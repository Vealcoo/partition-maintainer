package partition

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"partition-maintainer/internal/config"
)

type Manager struct {
	db  *sql.DB
	cfg config.Config
}

type tablePartitionInfo struct {
	PartitionMethod     string
	PartitionExpression sql.NullString
	PartitionCount      int
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func NewManager(db *sql.DB, cfg config.Config) *Manager {
	return &Manager{db: db, cfg: cfg}
}

func (m *Manager) Run(ctx context.Context, today time.Time) error {
	if err := validateIdentifier(m.cfg.TableName); err != nil {
		return fmt.Errorf("validate table name: %w", err)
	}

	info, err := m.getTablePartitionInfo(ctx)
	if err != nil {
		return fmt.Errorf("get table partition info: %w", err)
	}
	if err := validatePartitioning(info); err != nil {
		return err
	}

	existing, err := m.getExistingPartitions(ctx)
	if err != nil {
		return fmt.Errorf("get existing partitions: %w", err)
	}

	if !existing["pmax"] {
		return fmt.Errorf("table %s has no pmax partition; this project assumes pmax exists", m.cfg.TableName)
	}

	if err := m.ensureFuturePartitions(ctx, today, existing); err != nil {
		return fmt.Errorf("ensure future partitions: %w", err)
	}

	existing, err = m.getExistingPartitions(ctx)
	if err != nil {
		return fmt.Errorf("refresh partitions: %w", err)
	}

	if err := m.dropOldPartitions(ctx, today, existing); err != nil {
		return fmt.Errorf("drop old partitions: %w", err)
	}

	return nil
}

func (m *Manager) AcquireLock(ctx context.Context) (func() error, error) {
	if m.cfg.LockName == "" {
		return nil, nil
	}

	var ok sql.NullInt64
	if err := m.db.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", m.cfg.LockName, m.cfg.LockTimeoutSeconds).Scan(&ok); err != nil {
		return nil, fmt.Errorf("GET_LOCK failed: %w", err)
	}
	if !ok.Valid || ok.Int64 != 1 {
		return nil, fmt.Errorf("could not acquire lock %q within %d seconds", m.cfg.LockName, m.cfg.LockTimeoutSeconds)
	}

	log.Printf("acquired lock: %s", m.cfg.LockName)

	return func() error {
		var released sql.NullInt64
		if err := m.db.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", m.cfg.LockName).Scan(&released); err != nil {
			return fmt.Errorf("RELEASE_LOCK failed: %w", err)
		}
		log.Printf("released lock: %s", m.cfg.LockName)
		return nil
	}, nil
}

func (m *Manager) getExistingPartitions(ctx context.Context) (map[string]bool, error) {
	const query = `
SELECT PARTITION_NAME
FROM INFORMATION_SCHEMA.PARTITIONS
WHERE TABLE_SCHEMA = ?
  AND TABLE_NAME = ?
  AND PARTITION_NAME IS NOT NULL
`
	rows, err := m.db.QueryContext(ctx, query, m.cfg.DBName, m.cfg.TableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (m *Manager) ensureFuturePartitions(ctx context.Context, today time.Time, existing map[string]bool) error {
	var missing []time.Time

	for i := 0; i < m.cfg.CreateAheadDays; i++ {
		d := today.AddDate(0, 0, i)
		name := partitionName(d)
		if !existing[name] {
			missing = append(missing, d)
		}
	}

	if len(missing) == 0 {
		log.Printf("no partitions need to be created")
		return nil
	}
	if len(missing) > m.cfg.MaxCreatePartitionsPerRun {
		return fmt.Errorf("refusing to create %d partitions; limit is %d", len(missing), m.cfg.MaxCreatePartitionsPerRun)
	}

	sort.Slice(missing, func(i, j int) bool {
		return missing[i].Before(missing[j])
	})

	sqlText := buildReorganizePartitionSQL(m.cfg.TableName, missing)
	if m.cfg.DryRun {
		log.Printf("[dry-run] create SQL: %s", sqlText)
		return nil
	}

	log.Printf("creating %d partitions", len(missing))
	if _, err := m.db.ExecContext(ctx, sqlText); err != nil {
		return fmt.Errorf("exec create partition SQL: %w", err)
	}

	refreshed, err := m.getExistingPartitions(ctx)
	if err != nil {
		return fmt.Errorf("refresh partitions after create: %w", err)
	}
	if err := ensurePartitionsPresent(refreshed, missing); err != nil {
		return err
	}

	for _, d := range missing {
		log.Printf("created partition %s", partitionName(d))
	}

	return nil
}

func (m *Manager) dropOldPartitions(ctx context.Context, today time.Time, existing map[string]bool) error {
	cutoff := today.AddDate(0, 0, -m.cfg.DropBeforeDays)

	var toDrop []string
	for name := range existing {
		if name == "pmax" {
			continue
		}
		d, err := parsePartitionDate(name, today.Location())
		if err != nil {
			log.Printf("skip non-standard partition name: %s", name)
			continue
		}
		if d.Before(cutoff) {
			toDrop = append(toDrop, name)
		}
	}

	if len(toDrop) == 0 {
		log.Printf("no old partitions need to be dropped")
		return nil
	}
	if len(toDrop) > m.cfg.MaxDropPartitionsPerRun {
		return fmt.Errorf("refusing to drop %d partitions; limit is %d", len(toDrop), m.cfg.MaxDropPartitionsPerRun)
	}
	if len(toDrop) >= countDataPartitions(existing) {
		return fmt.Errorf("refusing to drop %d partitions because it would remove every non-pmax partition", len(toDrop))
	}

	sort.Strings(toDrop)
	sqlText := fmt.Sprintf("ALTER TABLE `%s` DROP PARTITION %s", m.cfg.TableName, joinQuotedPartitions(toDrop))

	if m.cfg.DryRun {
		log.Printf("[dry-run] drop SQL: %s", sqlText)
		return nil
	}

	log.Printf("dropping %d old partitions", len(toDrop))
	if _, err := m.db.ExecContext(ctx, sqlText); err != nil {
		return fmt.Errorf("exec drop partition SQL: %w", err)
	}

	refreshed, err := m.getExistingPartitions(ctx)
	if err != nil {
		return fmt.Errorf("refresh partitions after drop: %w", err)
	}
	if err := ensurePartitionsAbsent(refreshed, toDrop); err != nil {
		return err
	}
	if !refreshed["pmax"] {
		return fmt.Errorf("table %s lost pmax after drop", m.cfg.TableName)
	}

	for _, name := range toDrop {
		log.Printf("dropped partition %s", name)
	}

	return nil
}

func buildReorganizePartitionSQL(table string, days []time.Time) string {
	defs := make([]string, 0, len(days)+1)
	for _, d := range days {
		nextDay := d.AddDate(0, 0, 1)
		defs = append(defs, fmt.Sprintf(
			"PARTITION `%s` VALUES LESS THAN (TO_DAYS('%s'))",
			partitionName(d),
			nextDay.Format("2006-01-02"),
		))
	}
	defs = append(defs, "PARTITION `pmax` VALUES LESS THAN MAXVALUE")

	return fmt.Sprintf(
		"ALTER TABLE `%s` REORGANIZE PARTITION `pmax` INTO (%s)",
		table,
		strings.Join(defs, ", "),
	)
}

func partitionName(d time.Time) string {
	return "p" + d.Format("20060102")
}

func parsePartitionDate(name string, loc *time.Location) (time.Time, error) {
	if !strings.HasPrefix(name, "p") || len(name) != 9 {
		return time.Time{}, errors.New("invalid partition name")
	}
	return time.ParseInLocation("20060102", name[1:], loc)
}

func joinQuotedPartitions(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, p := range parts {
		quoted = append(quoted, fmt.Sprintf("`%s`", p))
	}
	return strings.Join(quoted, ", ")
}

func (m *Manager) getTablePartitionInfo(ctx context.Context) (tablePartitionInfo, error) {
	const query = `
SELECT COALESCE(PARTITION_METHOD, ''), PARTITION_EXPRESSION, COUNT(*)
FROM INFORMATION_SCHEMA.PARTITIONS
WHERE TABLE_SCHEMA = ?
  AND TABLE_NAME = ?
  AND PARTITION_NAME IS NOT NULL
GROUP BY PARTITION_METHOD, PARTITION_EXPRESSION
`

	var info tablePartitionInfo
	err := m.db.QueryRowContext(ctx, query, m.cfg.DBName, m.cfg.TableName).Scan(
		&info.PartitionMethod,
		&info.PartitionExpression,
		&info.PartitionCount,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return tablePartitionInfo{}, fmt.Errorf("table %s is not partitioned or does not exist", m.cfg.TableName)
		}
		return tablePartitionInfo{}, err
	}

	return info, nil
}

func validatePartitioning(info tablePartitionInfo) error {
	if !strings.EqualFold(info.PartitionMethod, "RANGE") {
		return fmt.Errorf("unsupported partition method %q; only RANGE is supported", info.PartitionMethod)
	}
	if info.PartitionCount < 2 {
		return fmt.Errorf("expected at least one data partition plus pmax, found %d partitions", info.PartitionCount)
	}
	return nil
}

func validateIdentifier(name string) error {
	if !identifierPattern.MatchString(name) {
		return fmt.Errorf("invalid identifier %q", name)
	}
	return nil
}

func ensurePartitionsPresent(existing map[string]bool, days []time.Time) error {
	for _, day := range days {
		name := partitionName(day)
		if !existing[name] {
			return fmt.Errorf("partition %s was not present after create DDL", name)
		}
	}
	if !existing["pmax"] {
		return fmt.Errorf("pmax was not present after create DDL")
	}
	return nil
}

func ensurePartitionsAbsent(existing map[string]bool, partitions []string) error {
	for _, name := range partitions {
		if existing[name] {
			return fmt.Errorf("partition %s is still present after drop DDL", name)
		}
	}
	return nil
}

func countDataPartitions(existing map[string]bool) int {
	count := 0
	for name := range existing {
		if name != "pmax" {
			count++
		}
	}
	return count
}
