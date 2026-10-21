package main

import (
	"testing"
	"time"
)

func TestPostgresUsageObservedLabel(t *testing.T) {
	at := time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		observed *time.Time
		fresh    bool
		want     string
	}{
		{nil, true, "never (no usage metered yet; nothing is overdue)"},
		{nil, false, "never"},
		{&at, true, "2026-10-14T21:00:00Z"},
	} {
		if got := postgresUsageObservedLabel(tc.observed, tc.fresh); got != tc.want {
			t.Errorf("label(%v, %t) = %q, want %q", tc.observed, tc.fresh, got, tc.want)
		}
	}
}
