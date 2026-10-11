// spec: §5 — cron fires at its scheduled minute.

package sched

import (
	"testing"
	"time"
)

// production-us hunt #5 (H5-40): a 60 s ticker started at an arbitrary second
// fired every * * * * * cron 46 s late on one node.
func TestUntilNextMinuteTickAlignsToTheBoundary(t *testing.T) {
	base := time.Date(2026, 10, 7, 18, 12, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Duration
		want time.Duration
	}{
		{0, time.Second},
		{500 * time.Millisecond, 500 * time.Millisecond},
		{time.Second, time.Minute},
		{46 * time.Second, 15 * time.Second},
		{59*time.Second + 900*time.Millisecond, 1100 * time.Millisecond},
	} {
		if got := untilNextMinuteTick(base.Add(tc.at)); got != tc.want {
			t.Fatalf("at +%s: wait %s, want %s", tc.at, got, tc.want)
		}
	}
}
