package partition

import (
	"strings"
	"testing"
	"time"
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
		PartitionMethod: "RANGE",
		PartitionCount:  2,
	})
	if err != nil {
		t.Fatalf("expected RANGE partitioning to pass, got %v", err)
	}

	err = validatePartitioning(tablePartitionInfo{
		PartitionMethod: "HASH",
		PartitionCount:  2,
	})
	if err == nil || !strings.Contains(err.Error(), "only RANGE is supported") {
		t.Fatalf("expected unsupported method error, got %v", err)
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
