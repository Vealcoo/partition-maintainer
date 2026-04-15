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

type partitionDef struct {
	Name            string
	Description     sql.NullString
	OrdinalPosition int
}

type forwardGapAnalysis struct {
	Repairable bool
	Missing    []time.Time
	UnsafeReason string
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
	if err := validatePartitioning(info, m.cfg.PartitionColumn); err != nil {
		return err
	}

	partitions, err := m.getPartitions(ctx)
	if err != nil {
		return fmt.Errorf("get partitions: %w", err)
	}
	if err := validatePartitionLayout(partitions, today.Location(), m.cfg.PartitionSpanDays); err != nil {
		return err
	}
	gap := analyzeForwardGap(partitions, today, m.cfg.PartitionSpanDays, m.cfg.CreateAheadDays)
	if gap.UnsafeReason != "" {
		return fmt.Errorf("table %s has an unsafe partition gap: %s", m.cfg.TableName, gap.UnsafeReason)
	}
	if len(gap.Missing) > 0 {
		if !gap.Repairable {
			return fmt.Errorf("table %s has an unsafe partition gap before the active maintenance window", m.cfg.TableName)
		}
		if !m.cfg.AutoRepairForwardGaps {
			return fmt.Errorf("table %s is missing %d forward partitions before the active maintenance window; set AUTO_REPAIR_FORWARD_GAPS=true to repair them", m.cfg.TableName, len(gap.Missing))
		}
		if err := m.repairForwardGap(ctx, gap.Missing); err != nil {
			return fmt.Errorf("repair forward gap: %w", err)
		}

		partitions, err = m.getPartitions(ctx)
		if err != nil {
			return fmt.Errorf("refresh partitions after repair: %w", err)
		}
		if err := validatePartitionLayout(partitions, today.Location(), m.cfg.PartitionSpanDays); err != nil {
			return err
		}
	}

	existing := partitionsByName(partitions)
	if !existing["pmax"] {
		return fmt.Errorf("table %s has no pmax partition; this project assumes pmax exists", m.cfg.TableName)
	}

	if err := m.ensureFuturePartitions(ctx, today, existing); err != nil {
		return fmt.Errorf("ensure future partitions: %w", err)
	}

	partitions, err = m.getPartitions(ctx)
	if err != nil {
		return fmt.Errorf("refresh partitions: %w", err)
	}
	if err := validatePartitionLayout(partitions, today.Location(), m.cfg.PartitionSpanDays); err != nil {
		return err
	}
	existing = partitionsByName(partitions)

	if err := m.dropOldPartitions(ctx, today, existing); err != nil {
		return fmt.Errorf("drop old partitions: %w", err)
	}

	return nil
}

func (m *Manager) repairForwardGap(ctx context.Context, missing []time.Time) error {
	if len(missing) == 0 {
		return nil
	}
	if len(missing) > m.cfg.MaxRepairPartitionsPerRun {
		return fmt.Errorf("refusing to repair %d partitions; limit is %d", len(missing), m.cfg.MaxRepairPartitionsPerRun)
	}

	sqlText := buildReorganizePartitionSQL(m.cfg.TableName, missing, m.cfg.PartitionSpanDays)
	if m.cfg.DryRun {
		log.Printf("[dry-run] repair SQL: %s", sqlText)
		return nil
	}

	log.Printf("repairing %d forward partitions", len(missing))
	if _, err := m.db.ExecContext(ctx, sqlText); err != nil {
		return fmt.Errorf("exec repair partition SQL: %w", err)
	}

	refreshedParts, err := m.getPartitions(ctx)
	if err != nil {
		return fmt.Errorf("refresh partitions after repair: %w", err)
	}
	refreshed := partitionsByName(refreshedParts)
	if err := ensurePartitionsPresent(refreshed, missing); err != nil {
		return err
	}

	for _, d := range missing {
		log.Printf("repaired partition %s", partitionName(d))
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

func (m *Manager) getPartitions(ctx context.Context) ([]partitionDef, error) {
	const query = `
SELECT PARTITION_NAME, PARTITION_DESCRIPTION, PARTITION_ORDINAL_POSITION
FROM INFORMATION_SCHEMA.PARTITIONS
WHERE TABLE_SCHEMA = ?
  AND TABLE_NAME = ?
  AND PARTITION_NAME IS NOT NULL
ORDER BY PARTITION_ORDINAL_POSITION
`
	rows, err := m.db.QueryContext(ctx, query, m.cfg.DBName, m.cfg.TableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []partitionDef
	for rows.Next() {
		var part partitionDef
		if err := rows.Scan(&part.Name, &part.Description, &part.OrdinalPosition); err != nil {
			return nil, err
		}
		result = append(result, part)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (m *Manager) ensureFuturePartitions(ctx context.Context, today time.Time, existing map[string]bool) error {
	var missing []time.Time

	start := partitionStart(today, m.cfg.PartitionSpanDays)
	end := partitionStart(today.AddDate(0, 0, m.cfg.CreateAheadDays-1), m.cfg.PartitionSpanDays)
	lastExisting, hasLastExisting, err := latestDataPartition(existing, today.Location())
	if err != nil {
		return err
	}
	for d := start; !d.After(end); d = d.AddDate(0, 0, m.cfg.PartitionSpanDays) {
		name := partitionName(d)
		if !existing[name] {
			if hasLastExisting && d.Before(lastExisting) {
				return fmt.Errorf("partition %s is missing before the latest existing data partition %s; refusing unsafe backfill", name, partitionName(lastExisting))
			}
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

	sqlText := buildReorganizePartitionSQL(m.cfg.TableName, missing, m.cfg.PartitionSpanDays)
	if m.cfg.DryRun {
		log.Printf("[dry-run] create SQL: %s", sqlText)
		return nil
	}

	log.Printf("creating %d partitions", len(missing))
	if _, err := m.db.ExecContext(ctx, sqlText); err != nil {
		return fmt.Errorf("exec create partition SQL: %w", err)
	}

	refreshedParts, err := m.getPartitions(ctx)
	if err != nil {
		return fmt.Errorf("refresh partitions after create: %w", err)
	}
	refreshed := partitionsByName(refreshedParts)
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
		end := d.AddDate(0, 0, m.cfg.PartitionSpanDays)
		if !end.After(cutoff) {
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

	refreshedParts, err := m.getPartitions(ctx)
	if err != nil {
		return fmt.Errorf("refresh partitions after drop: %w", err)
	}
	refreshed := partitionsByName(refreshedParts)
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

func buildReorganizePartitionSQL(table string, days []time.Time, spanDays int) string {
	defs := make([]string, 0, len(days)+1)
	for _, d := range days {
		nextDay := d.AddDate(0, 0, spanDays)
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

func validatePartitioning(info tablePartitionInfo, partitionColumn string) error {
	if !strings.EqualFold(info.PartitionMethod, "RANGE") {
		return fmt.Errorf("unsupported partition method %q; only RANGE is supported", info.PartitionMethod)
	}
	if info.PartitionCount < 2 {
		return fmt.Errorf("expected at least one data partition plus pmax, found %d partitions", info.PartitionCount)
	}
	if !info.PartitionExpression.Valid {
		return fmt.Errorf("partition expression is empty; expected TO_DAYS(%s)", quoteIdentifier(partitionColumn))
	}
	want := normalizePartitionExpression("TO_DAYS(" + quoteIdentifier(partitionColumn) + ")")
	if normalized := normalizePartitionExpression(info.PartitionExpression.String); normalized != want {
		return fmt.Errorf("unsupported partition expression %q; expected TO_DAYS(%s)", info.PartitionExpression.String, quoteIdentifier(partitionColumn))
	}
	return nil
}

func validatePartitionLayout(partitions []partitionDef, loc *time.Location, spanDays int) error {
	if len(partitions) < 2 {
		return fmt.Errorf("expected at least one data partition plus pmax, found %d partitions", len(partitions))
	}

	last := partitions[len(partitions)-1]
	if last.Name != "pmax" {
		return fmt.Errorf("last partition must be pmax, got %s", last.Name)
	}
	if !strings.EqualFold(last.Description.String, "MAXVALUE") {
		return fmt.Errorf("pmax partition must use MAXVALUE, got %q", last.Description.String)
	}

	var previousDay time.Time
	for i, part := range partitions[:len(partitions)-1] {
		day, err := parsePartitionDate(part.Name, loc)
		if err != nil {
			return fmt.Errorf("partition %s does not match expected pYYYYMMDD format", part.Name)
		}
		if !day.Equal(partitionStart(day, spanDays)) {
			return fmt.Errorf("partition %s is not aligned to PARTITION_SPAN_DAYS=%d", part.Name, spanDays)
		}

		wantBoundary := day.AddDate(0, 0, spanDays)
		if !part.Description.Valid {
			return fmt.Errorf("partition %s has empty PARTITION_DESCRIPTION", part.Name)
		}
		if part.Description.String != fmt.Sprintf("%d", toDaysNumber(wantBoundary)) {
			return fmt.Errorf("partition %s boundary mismatch: got %q, expected TO_DAYS('%s')", part.Name, part.Description.String, wantBoundary.Format("2006-01-02"))
		}

		if i > 0 && !day.Equal(previousDay.AddDate(0, 0, spanDays)) {
			return fmt.Errorf("partition sequence is not continuous between %s and %s", partitionName(previousDay), part.Name)
		}
		previousDay = day
	}

	return nil
}

func analyzeForwardGap(partitions []partitionDef, today time.Time, spanDays, createAheadDays int) forwardGapAnalysis {
	if len(partitions) < 2 {
		return forwardGapAnalysis{}
	}

	firstData := partitions[0]
	lastData := partitions[len(partitions)-2]
	firstDay, err := parsePartitionDate(firstData.Name, today.Location())
	if err != nil {
		return forwardGapAnalysis{}
	}
	lastDay, err := parsePartitionDate(lastData.Name, today.Location())
	if err != nil {
		return forwardGapAnalysis{}
	}

	windowStart := partitionStart(today, spanDays)
	windowEnd := partitionStart(today.AddDate(0, 0, createAheadDays-1), spanDays)
	if firstDay.After(windowStart) {
		return forwardGapAnalysis{
			UnsafeReason: fmt.Sprintf(
				"active window starts at %s but earliest partition is %s",
				partitionName(windowStart),
				firstData.Name,
			),
		}
	}
	nextExpected := lastDay.AddDate(0, 0, spanDays)
	if nextExpected.After(windowEnd) {
		return forwardGapAnalysis{}
	}

	missing := make([]time.Time, 0)
	for d := nextExpected; !d.After(windowEnd); d = d.AddDate(0, 0, spanDays) {
		missing = append(missing, d)
	}

	return forwardGapAnalysis{
		Repairable: true,
		Missing:    missing,
	}
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

func partitionsByName(partitions []partitionDef) map[string]bool {
	result := make(map[string]bool, len(partitions))
	for _, part := range partitions {
		result[part.Name] = true
	}
	return result
}

func latestDataPartition(existing map[string]bool, loc *time.Location) (time.Time, bool, error) {
	var latest time.Time
	found := false
	for name := range existing {
		if name == "pmax" {
			continue
		}
		day, err := parsePartitionDate(name, loc)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("parse partition %s: %w", name, err)
		}
		if !found || day.After(latest) {
			latest = day
			found = true
		}
	}
	return latest, found, nil
}

func normalizePartitionExpression(expr string) string {
	expr = strings.ToLower(expr)
	expr = strings.ReplaceAll(expr, "`", "")
	expr = strings.ReplaceAll(expr, " ", "")
	return expr
}

func quoteIdentifier(name string) string {
	return "`" + name + "`"
}

func partitionStart(day time.Time, spanDays int) time.Time {
	remainder := toDaysNumber(day) % spanDays
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location()).AddDate(0, 0, -remainder)
}

func toDaysNumber(t time.Time) int {
	year, month, day := t.Date()
	return mysqlToDays(year, int(month), day)
}

func mysqlToDays(year, month, day int) int {
	if year == 0 && month == 0 {
		return 0
	}

	days := 365*year + 31*(month-1) + day
	if month <= 2 {
		year--
	} else {
		days -= (month*4 + 23) / 10
	}

	return days + year/4 - ((year/100+1)*3)/4
}
