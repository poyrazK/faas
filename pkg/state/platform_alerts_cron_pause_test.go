package state

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The failing-cron pause read only the newest 10 runs and required the
// oldest of them to be 24 h old, so a cron firing more often than every
// 2.4 h could never pause: production's */15 chronolake cron failed 33 times
// in a row without a pause (async assessment 2026-11-28). The streak since
// the newest success now counts.
func TestMemPauseCronIfFailingCountsTheWholeStreak(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		interval time.Duration
		failures int
		success  int // index (newest first) of a successful run, -1 for none
		want     bool
	}{
		{name: "every 15 min for 25 h", interval: 15 * time.Minute, failures: 100, success: -1, want: true},
		{name: "hourly for 25 h", interval: time.Hour, failures: 25, success: -1, want: true},
		{name: "every 15 min for 3 h", interval: 15 * time.Minute, failures: 12, success: -1, want: false},
		{name: "success 2 h ago resets the streak", interval: 15 * time.Minute, failures: 100, success: 8, want: false},
		{name: "daily for 9 days", interval: 24 * time.Hour, failures: 9, success: -1, want: false},
		{name: "daily for 11 days", interval: 24 * time.Hour, failures: 11, success: -1, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMemStore()
			acct, err := m.CreateAccount(ctx, "pause@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "pause"})
			if err != nil {
				t.Fatal(err)
			}
			c, err := m.CreateCron(ctx, app.ID, "*/15 * * * *", "/tick", true)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			m.mu.Lock()
			for i := range tc.failures {
				at := now.Add(-time.Duration(i+1) * tc.interval)
				cronID := c.ID
				inv := Invocation{ID: fmt.Sprintf("inv-%d", i), AppID: app.ID, AccountID: acct.ID,
					CronID: &cronID, Source: InvocationCron, State: InvocationFailed, LastError: "HTTP 503",
					DueAt: at, CreatedAt: at, ReceivedAt: &at}
				if i == tc.success {
					inv.State, inv.LastError = InvocationCompleted, ""
				}
				m.invocations[inv.ID] = inv
			}
			m.mu.Unlock()
			got, err := m.PauseCronIfFailing(ctx, c.ID, api.CronFailurePauseRuns, api.CronFailurePauseSpan, api.PlatformAlertCooldown)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("paused = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCronFailureStreakPauses(t *testing.T) {
	old := time.Now().Add(-25 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	if !cronFailureStreakPauses(96, &old, 10, 24*time.Hour) {
		t.Fatal("a 25 h streak of 96 failures must pause")
	}
	if cronFailureStreakPauses(9, &old, 10, 24*time.Hour) {
		t.Fatal("fewer than 10 failures must not pause")
	}
	if cronFailureStreakPauses(96, &recent, 10, 24*time.Hour) {
		t.Fatal("a streak younger than 24 h must not pause")
	}
	if cronFailureStreakPauses(96, nil, 10, 24*time.Hour) {
		t.Fatal("no oldest run must not pause")
	}
}
