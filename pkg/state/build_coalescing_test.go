package state_test

// adr: 20261102100000 — a build that misses the cache while an identical
// build is building waits for it, then is claimed on the node that built it.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// coalescingStore is the store surface the coalescing contract uses; the
// MemStore and PgStore both implement it.
type coalescingStore interface {
	state.BuildCoalescingStore
	CreateBuild(context.Context, string, state.DeploymentKind, int64, string) (state.Build, error)
	ClaimQueuedBuild(context.Context, string) (state.Build, error)
	ClaimQueuedBuildWithNodeAffinity(context.Context, string, string, time.Duration) (state.Build, error)
	ClaimNextQueuedBuild(context.Context) (state.Build, error)
	SetBuildCacheOutcome(context.Context, string, string, string) error
	UpdateBuildStatus(context.Context, string, state.BuildStatus, state.FailureClass, bool, bool) error
	CreateBuildProvenance(context.Context, state.BuildProvenance) error
	BuildByID(context.Context, string) (state.Build, error)
}

func TestBuildCoalescing(t *testing.T) {
	t.Run("memstore", func(t *testing.T) {
		store := state.NewMemStore()
		app := seedMemAffinityApp(t, store)
		dep, err := store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		testBuildCoalescing(t, store, context.Background(), dep.ID)
	})
	t.Run("pgstore", func(t *testing.T) {
		s, ctx := pgStore(t)
		_, _, depID := seedLiveDeploy(t, s, ctx)
		testBuildCoalescing(t, s, ctx, depID)
	})
}

func testBuildCoalescing(t *testing.T, store coalescingStore, ctx context.Context, depID string) {
	t.Helper()
	key := strings.Repeat("ab", 32)
	create := func() state.Build {
		t.Helper()
		b, err := store.CreateBuild(ctx, depID, state.DeploymentKindTarball, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	claimMiss := func(id string) state.Build {
		t.Helper()
		c, err := store.ClaimQueuedBuild(ctx, id)
		if err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
		if err := store.SetBuildCacheOutcome(ctx, id, "miss", key); err != nil {
			t.Fatal(err)
		}
		return c
	}
	producer, waiter := create(), create()
	producerClaim := claimMiss(producer.ID)
	time.Sleep(5 * time.Millisecond)
	waiterClaim := claimMiss(waiter.ID)

	// Only the later claim waits, so two identical builds never wait for each other.
	if got, err := store.RunningIdenticalBuild(ctx, waiterClaim, key); err != nil || got != producer.ID {
		t.Fatalf("waiter's identical build = %q, %v; want %s", got, err, producer.ID)
	}
	if _, err := store.RunningIdenticalBuild(ctx, producerClaim, key); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("producer's identical build err = %v, want ErrNotFound", err)
	}
	if _, err := store.RunningIdenticalBuild(ctx, waiterClaim, strings.Repeat("cd", 32)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another key err = %v, want ErrNotFound", err)
	}

	// A stale claim is not deferred.
	stale := waiterClaim
	stale.StartedAt = stale.StartedAt.Add(-time.Second)
	if err := store.DeferBuildBehind(ctx, stale, producer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale defer err = %v, want ErrNotFound", err)
	}
	if err := store.DeferBuildBehind(ctx, waiterClaim, producer.ID); err != nil {
		t.Fatalf("defer: %v", err)
	}
	if got, err := store.BuildByID(ctx, waiter.ID); err != nil || got.Status != state.BuildQueued {
		t.Fatalf("deferred build = %+v, %v; want queued", got, err)
	}

	// While the producer builds, no claim takes the waiter; other work goes first.
	other := create()
	if _, err := store.ClaimQueuedBuild(ctx, waiter.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim of the waiter while its producer runs err = %v, want ErrNotFound", err)
	}
	next, err := store.ClaimNextQueuedBuild(ctx)
	if err != nil || next.ID != other.ID {
		t.Fatalf("next claim = %s, %v; want the unrelated build %s", next.ID, err, other.ID)
	}
	if _, err := store.ClaimNextQueuedBuild(ctx); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("next claim with only the waiter queued err = %v, want ErrNotFound", err)
	}

	// Once the producer is done, the node that built it claims the waiter at
	// once; another node waits for the affinity grace.
	if err := store.UpdateBuildStatus(ctx, producer.ID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	finished, err := store.BuildByID(ctx, producer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateBuildProvenance(ctx, state.BuildProvenance{
		BuildID: producer.ID, BuilderNodeID: "node-a", StartedAt: producerClaim.StartedAt, FinishedAt: finished.FinishedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueuedBuildWithNodeAffinity(ctx, waiter.ID, "node-b", time.Hour); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another node's claim within the grace err = %v, want ErrNotFound", err)
	}
	got, err := store.ClaimQueuedBuildWithNodeAffinity(ctx, waiter.ID, "node-a", time.Hour)
	if err != nil || got.ID != waiter.ID {
		t.Fatalf("producer node's claim = %s, %v; want the waiter", got.ID, err)
	}
}
