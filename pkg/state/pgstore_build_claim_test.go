package state_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgBuildClaimFairnessSkipsLockedRow(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)
	first, err := s.CreateBuild(ctx, depID, state.DeploymentKindTarball, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateBuild(ctx, depID, state.DeploymentKindTarball, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A direct notification claim is still uncommitted. The fairness worker
	// must skip it, not wait and subsequently return the same running build.
	if _, err := tx.Exec(ctx, "update builds set status='running', started_at=now() where id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	claimCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	got, err := s.ClaimNextQueuedBuildWithFairness(claimCtx, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != second.ID {
		t.Fatalf("claimed %s, want unlocked %s", got.ID, second.ID)
	}
	if _, err := s.ClaimNextQueuedBuildWithFairness(claimCtx, 30*time.Second); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty/unavailable queue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextQueuedBuildWithFairness(ctx, 30*time.Second); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("running row was claimed again: %v", err)
	}
}

func TestPgBuildClaimFairnessConcurrentOwners(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)
	const count = 16
	builds := make([]state.Build, count)
	for i := range builds {
		b, err := s.CreateBuild(ctx, depID, state.DeploymentKindTarball, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		builds[i] = b
	}
	start := make(chan struct{})
	ids := make(chan string, count*2)
	errs := make(chan error, count*2)
	var wg sync.WaitGroup
	for i := 0; i < count*2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var b state.Build
			var err error
			if i < count {
				b, err = s.ClaimQueuedBuild(ctx, builds[i].ID)
			} else {
				b, err = s.ClaimNextQueuedBuildWithFairness(ctx, time.Minute)
			}
			if err != nil {
				if !errors.Is(err, state.ErrNotFound) {
					errs <- fmt.Errorf("claim %d: %w", i, err)
				}
				return
			}
			ids <- b.ID
		}(i)
	}
	close(start)
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate owner for %s", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Errorf("claimed %d distinct builds, want %d", len(seen), count)
	}
}
