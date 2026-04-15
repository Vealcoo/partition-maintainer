package partition

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"partition-maintainer/internal/config"
)

func TestParsePartitionDateUsesProvidedLocation(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	got, err := parsePartitionDate("p20260414", loc)
	if err != nil {
		t.Fatalf("parse partition date: %v", err)
	}

	want := time.Date(2026, 4, 14, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	if got.Location() != loc {
		t.Fatalf("expected location %v, got %v", loc, got.Location())
	}
}

func TestPartitionNameRoundTripsAtLocalMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	day := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	name := partitionName(day)

	got, err := parsePartitionDate(name, loc)
	if err != nil {
		t.Fatalf("parse partition date: %v", err)
	}

	if !got.Equal(day) {
		t.Fatalf("expected %v, got %v", day, got)
	}
}

func TestValidateIdentifier(t *testing.T) {
	if err := validateIdentifier("user_events"); err != nil {
		t.Fatalf("expected valid identifier, got %v", err)
	}
	if err := validateIdentifier("user-events"); err == nil {
		t.Fatal("expected invalid identifier error")
	}
}

func TestValidatePartitioning(t *testing.T) {
	err := validatePartitioning(tablePartitionInfo{
		PartitionMethod:     "RANGE",
		PartitionExpression: nullString(" TO_DAYS(`event_date`) "),
		PartitionCount:      2,
	}, "event_date")
	if err != nil {
		t.Fatalf("expected RANGE partitioning to pass, got %v", err)
	}

	err = validatePartitioning(tablePartitionInfo{
		PartitionMethod:     "HASH",
		PartitionExpression: nullString("TO_DAYS(event_date)"),
		PartitionCount:      2,
	}, "event_date")
	if err == nil || !strings.Contains(err.Error(), "only RANGE is supported") {
		t.Fatalf("expected unsupported method error, got %v", err)
	}

	err = validatePartitioning(tablePartitionInfo{
		PartitionMethod:     "RANGE",
		PartitionExpression: nullString("TO_DAYS(created_at)"),
		PartitionCount:      2,
	}, "event_date")
	if err == nil || !strings.Contains(err.Error(), "expected TO_DAYS(`event_date`)") {
		t.Fatalf("expected unsupported expression error, got %v", err)
	}
}

func TestPartitionStartAlignsToSpanDays(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	day := time.Date(2026, 4, 15, 9, 30, 0, 0, loc)
	got := partitionStart(day, 7)
	want := time.Date(2026, 4, 11, 0, 0, 0, 0, loc)

	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestEnsurePartitionsPresent(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	days := []time.Time{time.Date(2026, 4, 14, 0, 0, 0, 0, loc)}

	err = ensurePartitionsPresent(map[string]bool{
		"p20260414": true,
		"pmax":      true,
	}, days)
	if err != nil {
		t.Fatalf("expected partitions to be present, got %v", err)
	}
}

func TestEnsurePartitionsAbsent(t *testing.T) {
	err := ensurePartitionsAbsent(map[string]bool{
		"pmax": true,
	}, []string{"p20260401"})
	if err != nil {
		t.Fatalf("expected partitions to be absent, got %v", err)
	}

	err = ensurePartitionsAbsent(map[string]bool{
		"p20260401": true,
		"pmax":      true,
	}, []string{"p20260401"})
	if err == nil {
		t.Fatal("expected error when partition still exists")
	}
}

func TestCountDataPartitions(t *testing.T) {
	got := countDataPartitions(map[string]bool{
		"p20260401": true,
		"p20260402": true,
		"pmax":      true,
	})
	if got != 2 {
		t.Fatalf("expected 2 data partitions, got %d", got)
	}
}

func TestValidatePartitionLayout(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	err = validatePartitionLayout([]partitionDef{
		{Name: "p20260414", Description: partitionBoundary("2026-04-15", loc), OrdinalPosition: 1},
		{Name: "p20260415", Description: partitionBoundary("2026-04-16", loc), OrdinalPosition: 2},
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 3},
	}, loc, 1)
	if err != nil {
		t.Fatalf("expected valid partition layout, got %v", err)
	}
}

func TestValidatePartitionLayoutRejectsGap(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	err = validatePartitionLayout([]partitionDef{
		{Name: "p20260414", Description: partitionBoundary("2026-04-15", loc), OrdinalPosition: 1},
		{Name: "p20260416", Description: partitionBoundary("2026-04-17", loc), OrdinalPosition: 2},
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 3},
	}, loc, 1)
	if err == nil || !strings.Contains(err.Error(), "not continuous") {
		t.Fatalf("expected continuity error, got %v", err)
	}
}

func TestValidatePartitionLayoutRejectsBoundaryMismatch(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	err = validatePartitionLayout([]partitionDef{
		{Name: "p20260414", Description: partitionBoundary("2026-04-16", loc), OrdinalPosition: 1},
		{Name: "p20260415", Description: partitionBoundary("2026-04-16", loc), OrdinalPosition: 2},
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 3},
	}, loc, 1)
	if err == nil || !strings.Contains(err.Error(), "boundary mismatch") {
		t.Fatalf("expected boundary mismatch error, got %v", err)
	}
}

func TestValidatePartitionLayoutRejectsInvalidPmax(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	err = validatePartitionLayout([]partitionDef{
		{Name: "p20260414", Description: partitionBoundary("2026-04-15", loc), OrdinalPosition: 1},
		{Name: "pmax", Description: partitionBoundary("2026-04-16", loc), OrdinalPosition: 2},
	}, loc, 1)
	if err == nil || !strings.Contains(err.Error(), "MAXVALUE") {
		t.Fatalf("expected invalid pmax error, got %v", err)
	}
}

func TestValidatePartitionLayoutRejectsMisalignedPartitionForSpan(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	err = validatePartitionLayout([]partitionDef{
		{Name: "p20260415", Description: partitionBoundary("2026-04-22", loc), OrdinalPosition: 1},
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 2},
	}, loc, 7)
	if err == nil || !strings.Contains(err.Error(), "not aligned") {
		t.Fatalf("expected alignment error, got %v", err)
	}
}

func TestBuildReorganizePartitionSQLUsesSpanBoundary(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	sqlText := buildReorganizePartitionSQL("user_events", []time.Time{
		time.Date(2026, 4, 11, 0, 0, 0, 0, loc),
	}, 7)
	if !strings.Contains(sqlText, "PARTITION `p20260411` VALUES LESS THAN (TO_DAYS('2026-04-18'))") {
		t.Fatalf("unexpected SQL: %s", sqlText)
	}
}

func TestAnalyzeForwardGapReturnsMissingBuckets(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	gap := analyzeForwardGap([]partitionDef{
		{Name: "p20260401", Description: partitionBoundary("2026-04-02", loc), OrdinalPosition: 1},
		{Name: "p20260402", Description: partitionBoundary("2026-04-03", loc), OrdinalPosition: 2},
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 3},
	}, time.Date(2026, 4, 15, 0, 0, 0, 0, loc), 1, 3)

	if !gap.Repairable {
		t.Fatal("expected gap to be repairable")
	}
	if len(gap.Missing) != 15 {
		t.Fatalf("expected 15 missing partitions, got %d", len(gap.Missing))
	}
	if partitionName(gap.Missing[0]) != "p20260403" {
		t.Fatalf("expected first missing partition p20260403, got %s", partitionName(gap.Missing[0]))
	}
	if partitionName(gap.Missing[len(gap.Missing)-1]) != "p20260417" {
		t.Fatalf("expected last missing partition p20260417, got %s", partitionName(gap.Missing[len(gap.Missing)-1]))
	}
}

func TestAnalyzeForwardGapReturnsEmptyWhenWindowCovered(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	gap := analyzeForwardGap([]partitionDef{
		{Name: "p20260414", Description: partitionBoundary("2026-04-15", loc), OrdinalPosition: 1},
		{Name: "p20260415", Description: partitionBoundary("2026-04-16", loc), OrdinalPosition: 2},
		{Name: "p20260416", Description: partitionBoundary("2026-04-17", loc), OrdinalPosition: 3},
		{Name: "p20260417", Description: partitionBoundary("2026-04-18", loc), OrdinalPosition: 4},
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 5},
	}, time.Date(2026, 4, 15, 0, 0, 0, 0, loc), 1, 3)

	if len(gap.Missing) != 0 {
		t.Fatalf("expected no missing partitions, got %d", len(gap.Missing))
	}
}

func TestAnalyzeForwardGapRejectsWindowBeforeEarliestPartition(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	gap := analyzeForwardGap([]partitionDef{
		{Name: "p20260501", Description: partitionBoundary("2026-05-02", loc), OrdinalPosition: 1},
		{Name: "p20260502", Description: partitionBoundary("2026-05-03", loc), OrdinalPosition: 2},
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 3},
	}, time.Date(2026, 4, 15, 0, 0, 0, 0, loc), 1, 30)

	if gap.Repairable {
		t.Fatal("expected gap to be unsafe")
	}
	if !strings.Contains(gap.UnsafeReason, "earliest partition is p20260501") {
		t.Fatalf("expected unsafe reason to mention earliest partition, got %q", gap.UnsafeReason)
	}
}

func TestEnsureFuturePartitionsRejectsBackfillBeforeLatestExistingPartition(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	m := &Manager{
		cfg: config.Config{
			TableName:                 "partition_test",
			PartitionSpanDays:         1,
			CreateAheadDays:           30,
			MaxCreatePartitionsPerRun: 30,
			DryRun:                    true,
		},
	}

	err = m.ensureFuturePartitions(context.Background(), time.Date(2026, 4, 15, 0, 0, 0, 0, loc), map[string]bool{
		"p20260501": true,
		"p20260502": true,
		"pmax":      true,
	})
	if err == nil || !strings.Contains(err.Error(), "refusing unsafe backfill") {
		t.Fatalf("expected unsafe backfill error, got %v", err)
	}
}

func TestEnsureFuturePartitionsRejectsBackfillBeforeLatestExistingPartitionForSpan(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	m := &Manager{
		cfg: config.Config{
			TableName:                 "partition_test",
			PartitionSpanDays:         7,
			CreateAheadDays:           21,
			MaxCreatePartitionsPerRun: 30,
			DryRun:                    true,
		},
	}

	err = m.ensureFuturePartitions(context.Background(), time.Date(2026, 4, 15, 0, 0, 0, 0, loc), map[string]bool{
		"p20260425": true,
		"pmax":      true,
	})
	if err == nil || !strings.Contains(err.Error(), "refusing unsafe backfill") {
		t.Fatalf("expected unsafe backfill error, got %v", err)
	}
}

func TestEnsureFuturePartitionsReturnsNilWhenWindowAlreadyCovered(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	m := &Manager{
		cfg: config.Config{
			TableName:                 "partition_test",
			PartitionSpanDays:         1,
			CreateAheadDays:           3,
			MaxCreatePartitionsPerRun: 30,
			DryRun:                    true,
		},
	}

	err = m.ensureFuturePartitions(context.Background(), time.Date(2026, 4, 15, 0, 0, 0, 0, loc), map[string]bool{
		"p20260415": true,
		"p20260416": true,
		"p20260417": true,
		"pmax":      true,
	})
	if err != nil {
		t.Fatalf("expected no-op create window, got %v", err)
	}
}

func TestAnalyzeForwardGapForSpanDays(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	gap := analyzeForwardGap([]partitionDef{
		{Name: "p20260411", Description: partitionBoundary("2026-04-18", loc), OrdinalPosition: 1},
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 2},
	}, time.Date(2026, 4, 15, 0, 0, 0, 0, loc), 7, 21)

	if !gap.Repairable {
		t.Fatal("expected span gap to be repairable")
	}
	if len(gap.Missing) != 3 {
		t.Fatalf("expected 3 missing span partitions, got %d", len(gap.Missing))
	}
	if partitionName(gap.Missing[0]) != "p20260418" {
		t.Fatalf("expected first missing span partition p20260418, got %s", partitionName(gap.Missing[0]))
	}
	if partitionName(gap.Missing[1]) != "p20260425" {
		t.Fatalf("expected second missing span partition p20260425, got %s", partitionName(gap.Missing[1]))
	}
	if partitionName(gap.Missing[2]) != "p20260502" {
		t.Fatalf("expected third missing span partition p20260502, got %s", partitionName(gap.Missing[2]))
	}
}

func TestLatestDataPartitionReturnsLatest(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	got, found, err := latestDataPartition(map[string]bool{
		"p20260401": true,
		"p20260409": true,
		"p20260403": true,
		"pmax":      true,
	}, loc)
	if err != nil {
		t.Fatalf("latestDataPartition: %v", err)
	}
	if !found {
		t.Fatal("expected latest partition to be found")
	}
	want := time.Date(2026, 4, 9, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestLatestDataPartitionRejectsInvalidPartitionName(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	_, _, err = latestDataPartition(map[string]bool{
		"bad_name": true,
		"pmax":     true,
	}, loc)
	if err == nil || !strings.Contains(err.Error(), "parse partition bad_name") {
		t.Fatalf("expected invalid partition parse error, got %v", err)
	}
}

func TestLatestDataPartitionReturnsNotFoundWhenOnlyPmaxExists(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	_, found, err := latestDataPartition(map[string]bool{
		"pmax": true,
	}, loc)
	if err != nil {
		t.Fatalf("latestDataPartition: %v", err)
	}
	if found {
		t.Fatal("expected no data partition to be found")
	}
}

func TestAnalyzeForwardGapReturnsEmptyWhenNoDataPartitions(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	gap := analyzeForwardGap([]partitionDef{
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 1},
	}, time.Date(2026, 4, 15, 0, 0, 0, 0, loc), 1, 30)
	if gap.Repairable || len(gap.Missing) != 0 || gap.UnsafeReason != "" {
		t.Fatalf("expected empty gap analysis, got %+v", gap)
	}
}

func TestAnalyzeForwardGapReturnsEmptyWhenLastPartitionNameIsInvalid(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	gap := analyzeForwardGap([]partitionDef{
		{Name: "bad_name", Description: partitionBoundary("2026-04-16", loc), OrdinalPosition: 1},
		{Name: "pmax", Description: nullString("MAXVALUE"), OrdinalPosition: 2},
	}, time.Date(2026, 4, 15, 0, 0, 0, 0, loc), 1, 30)
	if gap.Repairable || len(gap.Missing) != 0 || gap.UnsafeReason != "" {
		t.Fatalf("expected empty gap analysis, got %+v", gap)
	}
}

func TestRepairForwardGapRejectsRepairLimit(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	m := &Manager{
		cfg: config.Config{
			TableName:                  "partition_test",
			PartitionSpanDays:          1,
			MaxRepairPartitionsPerRun:  1,
			DryRun:                     true,
		},
	}

	err = m.repairForwardGap(context.Background(), []time.Time{
		time.Date(2026, 4, 15, 0, 0, 0, 0, loc),
		time.Date(2026, 4, 16, 0, 0, 0, 0, loc),
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to repair 2 partitions") {
		t.Fatalf("expected repair limit error, got %v", err)
	}
}

func TestRepairForwardGapDryRunAllowsSinglePartition(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	m := &Manager{
		cfg: config.Config{
			TableName:                  "partition_test",
			PartitionSpanDays:          1,
			MaxRepairPartitionsPerRun:  1,
			DryRun:                     true,
		},
	}

	err = m.repairForwardGap(context.Background(), []time.Time{
		time.Date(2026, 4, 15, 0, 0, 0, 0, loc),
	})
	if err != nil {
		t.Fatalf("expected dry-run repair to succeed, got %v", err)
	}
}

func TestDropOldPartitionsSelectsWholeExpiredSpanPartitionsOnly(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	m := &Manager{
		cfg: config.Config{
			TableName:               "partition_test",
			PartitionSpanDays:       7,
			DropBeforeDays:          10,
			MaxDropPartitionsPerRun: 30,
			DryRun:                  true,
		},
	}

	err = m.dropOldPartitions(context.Background(), time.Date(2026, 4, 15, 0, 0, 0, 0, loc), map[string]bool{
		"p20260328": true,
		"p20260404": true,
		"p20260411": true,
		"pmax":      true,
	})
	if err != nil {
		t.Fatalf("expected dry-run drop selection to succeed, got %v", err)
	}
}

func TestDropOldPartitionsRejectsDroppingAllDataPartitions(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	m := &Manager{
		cfg: config.Config{
			TableName:               "partition_test",
			PartitionSpanDays:       1,
			DropBeforeDays:          90,
			MaxDropPartitionsPerRun: 30,
			DryRun:                  true,
		},
	}

	err = m.dropOldPartitions(context.Background(), time.Date(2026, 4, 15, 0, 0, 0, 0, loc), map[string]bool{
		"p20260101": true,
		"pmax":      true,
	})
	if err == nil || !strings.Contains(err.Error(), "would remove every non-pmax partition") {
		t.Fatalf("expected all-data-partitions guard error, got %v", err)
	}
}

func TestDropOldPartitionsRejectsDropLimit(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	m := &Manager{
		cfg: config.Config{
			TableName:               "partition_test",
			PartitionSpanDays:       1,
			DropBeforeDays:          90,
			MaxDropPartitionsPerRun: 1,
			DryRun:                  true,
		},
	}

	err = m.dropOldPartitions(context.Background(), time.Date(2026, 4, 15, 0, 0, 0, 0, loc), map[string]bool{
		"p20260101": true,
		"p20260102": true,
		"p20260415": true,
		"pmax":      true,
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to drop 2 partitions") {
		t.Fatalf("expected drop limit error, got %v", err)
	}
}

func TestNormalizePartitionExpression(t *testing.T) {
	got := normalizePartitionExpression(" TO_DAYS ( `created_at` ) ")
	if got != "to_days(created_at)" {
		t.Fatalf("expected normalized expression, got %q", got)
	}
}

func TestQuoteIdentifier(t *testing.T) {
	if got := quoteIdentifier("created_at"); got != "`created_at`" {
		t.Fatalf("expected quoted identifier, got %q", got)
	}
}

func TestMysqlToDays(t *testing.T) {
	if got := mysqlToDays(2026, 5, 2); got != toDaysNumber(time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected mysqlToDays to match toDaysNumber, got %d", got)
	}
}

func TestMysqlToDaysZeroDate(t *testing.T) {
	if got := mysqlToDays(0, 0, 1); got != 0 {
		t.Fatalf("expected zero-style date to map to 0, got %d", got)
	}
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

func partitionBoundary(day string, loc *time.Location) sql.NullString {
	t, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		panic(err)
	}
	return nullString(fmt.Sprintf("%d", toDaysNumber(t)))
}
