package main

import (
	"strings"
	"testing"
	"time"
)

func TestDrillMarkCommitsBeforeReadingTheClock(t *testing.T) {
	const drill = "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b"
	sql := drillMarkSQL(drill)
	commit, clock := strings.Index(sql, "COMMIT;"), strings.LastIndex(sql, "clock_timestamp()")
	if !strings.HasPrefix(sql, "BEGIN;") || commit < 0 || clock < commit {
		t.Fatalf("the clock is not read after the mark commits: %s", sql)
	}
	if !strings.Contains(sql, "'"+drill+"'") || !strings.Contains(sql, "synchronous_commit = local") {
		t.Fatalf("mark = %s", sql)
	}
	at, err := parseDrillClock("1760970000123456\n")
	if err != nil || !at.Equal(time.UnixMicro(1760970000123456)) || at.Location() != time.UTC {
		t.Fatalf("clock = %v, %v", at, err)
	}
	for _, bad := range []string{"", "0", "-5", "12.5", "now"} {
		if _, err := parseDrillClock(bad); err == nil {
			t.Fatalf("parsed clock %q", bad)
		}
	}
}
