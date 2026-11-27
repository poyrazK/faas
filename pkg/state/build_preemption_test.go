package state_test

// adr: 20261118154632 — a build a borrowed builder slot gives back returns to
// the queue at its place and counts the give-back.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// preemptionStore is the store surface the preemption contract uses; the
// MemStore and PgStore both implement it.
type preemptionStore interface {
	state.BuildPreemptionStore
	CreateBuild(context.Context, string, state.DeploymentKind, int64, string) (state.Build, error)
	ClaimQueuedBuild(context.Context, string) (state.Build, error)
	BuildByID(context.Context, string) (state.Build, error)
}

func TestBuildPreemption(t *testing.T) {
	t.Run("memstore", func(t *testing.T) {
		store := state.NewMemStore()
		app := seedMemAffinityApp(t, store)
		dep, err := store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		testBuildPreemption(t, store, context.Background(), dep.ID)
	})
	t.Run("pgstore", func(t *testing.T) {
		s, ctx := pgStore(t)
		_, _, depID := seedLiveDeploy(t, s, ctx)
		testBuildPreemption(t, s, ctx, depID)
	})
}

func testBuildPreemption(t *testing.T, store preemptionStore, ctx context.Context, depID string) {
	t.Helper()
	build, err := store.CreateBuild(ctx, depID, state.DeploymentKindTarball, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := store.BuildPreemptions(ctx, build.ID); err != nil || n != 0 {
		t.Fatalf("new build preemptions = %d, %v; want 0", n, err)
	}
	queued, err := store.BuildByID(ctx, build.ID)
	if err != nil {
		t.Fatal(err)
	}

	for want := 1; want <= 2; want++ {
		claim, err := store.ClaimQueuedBuild(ctx, build.ID)
		if err != nil {
			t.Fatalf("claim %d: %v", want, err)
		}
		if err := store.RequeuePreemptedBuild(ctx, claim); err != nil {
			t.Fatalf("requeue %d: %v", want, err)
		}
		got, err := store.BuildByID(ctx, build.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != state.BuildQueued || !got.StartedAt.IsZero() {
			t.Fatalf("after give-back %d: status %s started_at %v; want queued with no start", want, got.Status, got.StartedAt)
		}
		if !got.EnqueuedAt.Equal(queued.EnqueuedAt) {
			t.Fatalf("after give-back %d: enqueued_at %v, want %v (the build keeps its place)", want, got.EnqueuedAt, queued.EnqueuedAt)
		}
		if n, err := store.BuildPreemptions(ctx, build.ID); err != nil || n != want {
			t.Fatalf("preemptions after give-back %d = %d, %v", want, n, err)
		}
		// A stale worker holding this claim cannot requeue it again.
		if err := store.RequeuePreemptedBuild(ctx, claim); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("stale requeue %d err = %v, want ErrNotFound", want, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if _, err := store.BuildPreemptions(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing build preemptions err = %v, want ErrNotFound", err)
	}
}
