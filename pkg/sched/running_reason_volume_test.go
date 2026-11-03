// adr: 20261102205122 — the "why is it running" recorder writes an event when
// an explanation changes, not on every request, and refreshes an unchanged one
// every few minutes.
package sched_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRunningReasonIgnoresRequestEvidenceButRefreshes(t *testing.T) {
	store := state.NewMemStore()
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, nil)
	appID, accountID := uuid.NewString(), uuid.NewString()
	apps := []state.App{{ID: appID, AccountID: accountID}}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	observe := func(at time.Time, lastRequest time.Time, openConns int64) int {
		t.Helper()
		snapshot := []sched.InstanceInfo{{Instance: "i-1", AppID: appID, Plan: api.PlanPro, State: state.StateRunning,
			LastRequest: lastRequest, Started: now.Add(-time.Hour), IdleTimeoutS: 60, OpenConns: openConns}}
		loop.RecordRunningReasonObservations(t.Context(), apps, snapshot, at)
		events, err := store.ListEvents(t.Context(), appID, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(events)
	}

	if got := observe(now, now.Add(-5*time.Second), 0); got != 1 {
		t.Fatalf("first observation wrote %d events, want 1", got)
	}
	// A steady stream of requests moves the last activity every tick.
	for i := 1; i <= 10; i++ {
		at := now.Add(time.Duration(i) * 20 * time.Second)
		if got := observe(at, at.Add(-time.Second), 0); got != 1 {
			t.Fatalf("request %d wrote a new event (%d events); new request evidence alone must not", i, got)
		}
	}
	// The explanation is unchanged, but its evidence is refreshed.
	refresh := now.Add(6 * time.Minute)
	if got := observe(refresh, refresh.Add(-time.Second), 0); got != 2 {
		t.Fatalf("events after the refresh interval = %d, want 2", got)
	}
	// A new cause still records at once (past the 15 s throttle).
	changed := refresh.Add(20 * time.Second)
	if got := observe(changed, changed.Add(-time.Second), 2); got != 3 {
		t.Fatalf("events after a new open-connection cause = %d, want 3", got)
	}
}
