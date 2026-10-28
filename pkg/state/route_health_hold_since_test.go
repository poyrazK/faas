package state_test

// adr: 20261028110044

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
)

// A hold keeps the time of its first blocking decision while its context
// lasts, also when it turns from unknown to regressed, so the hold deadline
// counts the whole hold; a resume or a new stage starts a new status.
func TestRouteHealthHoldSince(t *testing.T) {
	entry := notificationEntry("unknown")
	start := entry.CheckedAt
	var st state.RouteHealthNotificationState
	for i, step := range []struct {
		status, stateStatus string
		nextStage           bool
		wantSince           time.Duration
	}{
		{status: "unknown", stateStatus: "blocked_unknown", wantSince: 0},
		{status: "unknown", stateStatus: "blocked_unknown", wantSince: 0},
		{status: "regressed", stateStatus: "blocked_regressed", wantSince: 0},
		{status: "healthy", stateStatus: "clear", wantSince: 3 * time.Minute},
		{status: "regressed", stateStatus: "blocked_regressed", nextStage: true, wantSince: 4 * time.Minute},
	} {
		entry.CheckedAt = start.Add(time.Duration(i) * time.Minute)
		entry.Report.Status = step.status
		if step.nextStage {
			entry.Report.CanaryStep++
		}
		entry.Decision = routehealth.Decision(entry.Report)
		next, err := state.RouteHealthTransition(st, entry, "demo")
		if err != nil {
			t.Fatal(err)
		}
		st = next.State
		if st.Status != step.stateStatus || !st.Since.Equal(start.Add(step.wantSince)) {
			t.Fatalf("step %d: status %s since %s, want %s since %s", i, st.Status, st.Since, step.stateStatus, start.Add(step.wantSince))
		}
	}
}
