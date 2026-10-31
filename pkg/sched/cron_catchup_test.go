package sched_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// After a month without ticks, a per-minute cron's catch-up keeps only the
// newest api.CronCatchUpRecordedMax due times, and the newest is the latest
// minute at or before now; a short gap keeps every due time.
func TestCronDueOccurrencesKeepsTheNewest(t *testing.T) {
	every, err := sched.ParseScheduleWithTimezone("* * * * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 12, 34, 30, 0, time.UTC)
	first := every.NextFireAt(now.Add(-30 * 24 * time.Hour))

	due, skipped := sched.XCronDueOccurrences(every, first, now, api.CronCatchUpRecordedMax)
	if len(due) != api.CronCatchUpRecordedMax {
		t.Fatalf("kept %d due times, want %d", len(due), api.CronCatchUpRecordedMax)
	}
	if want := time.Date(2026, 10, 30, 12, 34, 0, 0, time.UTC); !due[len(due)-1].Equal(want) {
		t.Fatalf("newest due time %v, want %v", due[len(due)-1], want)
	}
	if total := 30 * 24 * 60; skipped+len(due) != total {
		t.Fatalf("kept %d + passed over %d = %d due times, want %d", len(due), skipped, skipped+len(due), total)
	}
	for i := 1; i < len(due); i++ {
		if !due[i].After(due[i-1]) {
			t.Fatalf("due times out of order at %d: %v", i, due)
		}
	}

	due, skipped = sched.XCronDueOccurrences(every, now.Add(-150*time.Second).Truncate(time.Minute).Add(time.Minute), now, api.CronCatchUpRecordedMax)
	if len(due) != 2 || skipped != 0 {
		t.Fatalf("a two-minute gap kept %d and passed over %d, want 2 and 0", len(due), skipped)
	}
}
