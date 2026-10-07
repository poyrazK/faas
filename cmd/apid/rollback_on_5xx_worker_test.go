package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// rollbackOn5xxTestStore adds the Postgres-only request summary to MemStore.
type rollbackOn5xxTestStore struct {
	*state.MemStore
	requests, serverErrors int64
	reads                  int
}

func (s *rollbackOn5xxTestStore) RequestTelemetryCircuitBreakerSummary(_ context.Context, _, _ string, _, _ time.Time) (int64, int64, float64, int64, float64, int64, int64, error) {
	s.reads++
	return s.requests, s.serverErrors, 0, 0, 0, 0, 0, nil
}

func TestRollbackOn5xxBreached(t *testing.T) {
	for _, tc := range []struct {
		requests, serverErrors int64
		want                   bool
	}{
		{requests: 30, serverErrors: 30, want: true},
		{requests: 10, serverErrors: 5, want: true},
		{requests: 4, serverErrors: 4, want: false},     // too few to judge
		{requests: 1000, serverErrors: 10, want: false}, // one bad route on a busy release
		{requests: 0, serverErrors: 0, want: false},
	} {
		if got := rollbackOn5xxBreached(tc.requests, tc.serverErrors); got != tc.want {
			t.Errorf("rollbackOn5xxBreached(%d, %d) = %v, want %v", tc.requests, tc.serverErrors, got, tc.want)
		}
	}
}

// production-us hunt #4 (H4-26): `deploy --rollback-on-5xx` stored the opt-in
// and nothing read it, so v8 answered 100% 500s and stayed live. The worker
// opens the first-wake window on first traffic, leaves a healthy release
// alone, and hands a failing one's predecessor to the readiness-gated rollback.
func TestRollbackOn5xxSweepRollsBackAFailingRelease(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	previous := mustSeedDeployment(t, e, "r5-app")
	if err := e.store.MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	app, _ := e.store.AppBySlug(ctx, "r5-app")
	release, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + repeat("f", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployBuilding, CreatedAt: time.Now().UTC().Add(time.Second), RollbackOn5xx: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, release.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentSuperseded(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	store := &rollbackOn5xxTestStore{MemStore: e.store}
	e.s.store = store
	read := func(id string) state.Deployment {
		t.Helper()
		d, err := e.store.DeploymentByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	sweep := func() {
		t.Helper()
		if err := e.s.rollbackOn5xxSweep(ctx, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}

	sweep() // no traffic yet
	if got := read(release.ID); got.FirstWakeAt != nil || got.Status != state.DeployLive {
		t.Fatalf("no traffic: release = status %s first_wake %v, want live with no window", got.Status, got.FirstWakeAt)
	}

	store.requests, store.serverErrors = 40, 2
	sweep() // healthy traffic opens the window and changes nothing else
	if got := read(release.ID); got.FirstWakeAt == nil || got.First5xxWindowEndsAt == nil || got.Status != state.DeployLive || got.LastAutoRollbackReason != "" {
		t.Fatalf("healthy traffic: release = %+v, want live with an open window", got)
	}
	if got := read(previous.ID); got.Status != state.DeploySuperseded {
		t.Fatalf("healthy traffic moved the predecessor to %s", got.Status)
	}

	store.requests, store.serverErrors = 40, 38
	sweep()
	if got := read(release.ID); got.LastAutoRollbackReason != string(state.AutoRollbackReasonThresholdExceeded) {
		t.Fatalf("failing release reason = %q, want %q", got.LastAutoRollbackReason, state.AutoRollbackReasonThresholdExceeded)
	}
	if got := read(previous.ID); got.Status != state.DeploySnapshotting {
		t.Fatalf("predecessor status = %s, want the readiness-gated %s", got.Status, state.DeploySnapshotting)
	}

	reads := store.reads
	sweep()
	if store.reads != reads {
		t.Fatalf("a rolled-back release was evaluated again (%d -> %d telemetry reads)", reads, store.reads)
	}
}
