package main

import (
	"testing"
	"time"
)

func TestLocalDayStartUsesCalendarMidnightInLocation(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	now := time.Date(2026, 4, 14, 15, 45, 30, 0, time.UTC)
	got := localDayStart(now, loc)
	want := time.Date(2026, 4, 14, 0, 0, 0, 0, loc)

	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	if got.Location() != loc {
		t.Fatalf("expected location %v, got %v", loc, got.Location())
	}
}
