package state_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreAccountDeployRateFixedWindow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "deploy-rate@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.FixedZone("test", 3*60*60))

	for i := 1; i <= 10; i++ {
		got, err := store.ConsumeAccountDeployRate(ctx, acct.ID, 10, now)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Allowed || got.Used != i || got.Remaining != 10-i {
			t.Fatalf("consume %d = %+v", i, got)
		}
	}
	blocked, err := store.ConsumeAccountDeployRate(ctx, acct.ID, 10, now.Add(59*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Allowed || blocked.Used != 10 || blocked.Remaining != 0 {
		t.Fatalf("blocked consume = %+v", blocked)
	}
	if want := now.UTC().Add(time.Hour); !blocked.WindowResetsAt.Equal(want) {
		t.Fatalf("reset = %s, want %s", blocked.WindowResetsAt, want)
	}

	reset, err := store.ConsumeAccountDeployRate(ctx, acct.ID, 10, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !reset.Allowed || reset.Used != 1 || reset.Remaining != 9 {
		t.Fatalf("reset consume = %+v", reset)
	}
}

func TestMemStoreAccountDeployRateConcurrentAdmission(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "deploy-race@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	const attempts = 64
	const limit = 10
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := store.ConsumeAccountDeployRate(ctx, acct.ID, limit, now)
			if err != nil {
				t.Errorf("consume: %v", err)
				return
			}
			if got.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != limit {
		t.Fatalf("allowed = %d, want %d", got, limit)
	}
	snapshot, err := store.ReadAccountDeployRate(ctx, acct.ID, limit, now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Used != limit || snapshot.Remaining != 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
