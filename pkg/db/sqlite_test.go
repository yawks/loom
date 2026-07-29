package db

import (
	"testing"
	"time"
)

func TestParseTimeMillisPreservesMillisecondPrecision(t *testing.T) {
	timestamp := "2026-07-29 10:11:12.345678901+00:00"
	want := time.Date(2026, 7, 29, 10, 11, 12, 345000000, time.UTC).UnixMilli()

	if got := ParseTimeMillis(timestamp); got != want {
		t.Fatalf("ParseTimeMillis(%q) = %d, want %d", timestamp, got, want)
	}
}

func TestParseTimeMillisConvertsNumericUnixSeconds(t *testing.T) {
	const seconds int64 = 1_785_319_872
	if got, want := ParseTimeMillis(seconds), seconds*1000; got != want {
		t.Fatalf("ParseTimeMillis(%d) = %d, want %d", seconds, got, want)
	}
}
