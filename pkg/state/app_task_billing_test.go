package state

import (
	"testing"
	"time"
)

// Cost-control review 2026-11-19 (N16): an app task VM bills the seconds it
// held within the sampled minute, bounded when it never records a finish.
func TestAppTaskBillableSeconds(t *testing.T) {
	minute := time.Date(2026, 11, 20, 10, 5, 0, 0, time.UTC)
	end := minute.Add(time.Minute)
	at := func(d time.Duration) *time.Time { v := minute.Add(d); return &v }
	for _, tc := range []struct {
		name string
		w    AppTaskBillingWindow
		now  time.Time
		want int64
	}{
		{"whole minute while running", AppTaskBillingWindow{Status: AppTaskRunning, StartedAt: minute.Add(-time.Hour / 2), TimeoutSeconds: 3600}, end.Add(time.Second), 60},
		{"starts inside the minute", AppTaskBillingWindow{Status: AppTaskRunning, StartedAt: minute.Add(20 * time.Second), TimeoutSeconds: 600}, end, 40},
		{"finishes inside the minute", AppTaskBillingWindow{Status: AppTaskSucceeded, StartedAt: minute.Add(-time.Minute), FinishedAt: at(15 * time.Second), TimeoutSeconds: 600}, end, 15},
		{"finished before the minute", AppTaskBillingWindow{Status: AppTaskSucceeded, StartedAt: minute.Add(-2 * time.Minute), FinishedAt: at(-time.Second), TimeoutSeconds: 600}, end, 0},
		{"terminal without a finish ends at its update", AppTaskBillingWindow{Status: AppTaskFailed, StartedAt: minute.Add(-time.Minute), UpdatedAt: minute.Add(10 * time.Second), TimeoutSeconds: 600}, end, 10},
		{"expired lease bounds a lost runner", AppTaskBillingWindow{Status: AppTaskRunning, StartedAt: minute.Add(-time.Minute), LeaseExpiresAt: at(30 * time.Second), TimeoutSeconds: 600}, end.Add(time.Hour), 30},
		{"timeout plus slack bounds a task with no lease", AppTaskBillingWindow{Status: AppTaskRunning, StartedAt: minute.Add(-AppTaskBillingSlack - 45*time.Second), TimeoutSeconds: 60}, end.Add(time.Hour), 15},
		{"the live minute stops at now", AppTaskBillingWindow{Status: AppTaskRunning, StartedAt: minute.Add(-time.Minute), TimeoutSeconds: 600}, minute.Add(25 * time.Second), 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.w.BillableSeconds(minute, end, tc.now); got != tc.want {
				t.Fatalf("BillableSeconds = %d, want %d", got, tc.want)
			}
		})
	}
}
