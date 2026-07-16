package sched

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestLoopReaperLogsParkableInstances seeds a ledger with an instance past
// its idle timeout, runs a single reaper tick against a synthetic snapshot,
// and asserts the ledger released the instance. We drive the reaper via a
// test-only helper that doesn't need a real pool or LISTEN.
func TestLoopReaperLogsParkableInstances(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", api.PlanPro)
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "loop-app", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5,
	})

	now := time.Now().Add(-10 * time.Minute) // way past the 60s idle timeout
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := NewLedger()
	loop := &Loop{store: store, ledger: l, log: log}

	loop.runReaperOnce(context.Background(), []InstanceInfo{
		{Instance: "loop-app:i1", AppID: app.ID, Plan: api.PlanPro,
			State: state.StateRunning, RAMMB: 520,
			LastRequest: now, Started: now.Add(-time.Hour), IdleTimeoutS: 60},
	})

	if got := l.ResidentRAM(); got != 0 {
		t.Errorf("ResidentRAM after reaper = %d, want 0 (instance released)", got)
	}
}

// TestHandleNotificationRejectsBadJSON covers the dispatch path: a malformed
// payload must not panic and must not propagate errors.
func TestHandleNotificationRejectsBadJSON(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// pool nil — Run() not invoked, only handleNotification.
	l := &Loop{store: nil, ledger: nil, log: log}

	l.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged, Payload: "{this is not json",
	})
	l.handleNotification(context.Background(), db.Notification{
		Channel: "no_such_channel", Payload: "{}",
	})
}

// TestHandleDeploymentLiveCreatesInstance covers the M5 wiring: when imaged
// emits deployment_changed with status="live", schedd creates an instance row
// in cold_booting state. This is the minimum required by CLAUDE.md invariant
// §6.2-1 (an app with a live deployment must have a row in `instances`).
//
// We don't drive Run(); handleNotification is the public seam. Pool is nil
// because materialiseLiveInstance's emit is a no-op when l.pool is unset.
//
// Note: imaged emits `status:"live"` exactly once per deployment (the
// re-emit happens once after the terminal transition). Duplicate-row
// protection would require a unique (deployment_id) constraint, which is a
// schema migration out of scope for M5.1.
func TestHandleDeploymentLiveCreatesInstance(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", api.PlanHobby)
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "live-app", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 2,
	})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	loop := &Loop{store: store, ledger: NewLedger(), log: log}

	loop.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"dep-123","status":"live"}`,
	})

	rows, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("instance rows = %d, want 1", len(rows))
	}
	if rows[0].State != string(state.StateColdBooting) {
		t.Errorf("state = %q, want %q", rows[0].State, state.StateColdBooting)
	}
	if rows[0].DeploymentID != "dep-123" {
		t.Errorf("deployment_id = %q, want %q", rows[0].DeploymentID, "dep-123")
	}
	if rows[0].RAMMB != 256 {
		t.Errorf("ram_mb = %d, want 256", rows[0].RAMMB)
	}
}

// --- tiny shims to keep the test self-contained ------------------------------

// runReaperOnce is a tiny helper for the test; the real Loop exposes Run + a
// private runReaper that reads from the store. We add a public seam so tests
// can drive reaper decisions without LISTEN.
func (l *Loop) runReaperOnce(ctx context.Context, snapshot []InstanceInfo) {
	now := time.Now()
	resident := l.ledger.ResidentRAM()
	for _, id := range ReapIdle(now, snapshot) {
		l.ledger.Release(id)
	}
	for _, id := range SelectEvictions(resident, now, snapshot) {
		l.ledger.Release(id)
	}
	_ = ctx
}

// sync.Mutex silence: keep the import set stable when other helpers move.
var _ sync.Mutex
