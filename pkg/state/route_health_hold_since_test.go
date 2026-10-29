package state_test

// adr: 20261028110044

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

// A hold on evidence a gateway may have lost starts no customer-visible hold
// and sends no blocked webhook (ADR-20261028110044 amendment 1).
func TestRouteHealthTransitionIgnoresLostEvidence(t *testing.T) {
	entry := notificationEntry("unknown")
	entry.Report.Routes = []api.RouteHealthFinding{{Method: "POST", Path: "/checkout", Windows: routehealth.Windows(entry.CheckedAt)}}
	routehealth.Evaluate(&entry.Report, entry.Report.ObservationAnchor, routehealth.TelemetryIncomplete)
	entry.Decision = routehealth.Decision(entry.Report)
	if entry.Decision.Status != "blocked" || !routehealth.EvidenceLost(entry.Report) {
		t.Fatalf("fixture decision %+v is not a hold on lost evidence", entry.Decision)
	}
	next, err := state.RouteHealthTransition(state.RouteHealthNotificationState{}, entry, "demo")
	if err != nil || next.Event != "" || next.State.Status != "clear" || len(next.Payload) != 0 {
		t.Fatalf("lost evidence transition = %+v %v; want no event and no hold", next, err)
	}
}
