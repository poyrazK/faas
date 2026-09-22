package state_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGConcurrencyQueueAdmissionEnforcesFleetCapAndReclaimsExpiry(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, store, ctx, "-queue-lease", "queue-lease")
	firstReplica := state.NewPGConcurrencyQueueAdmission(pool)
	secondReplica := state.NewPGConcurrencyQueueAdmission(pool)

	start := make(chan struct{})
	type result struct {
		leaseID string
		depth   int
		ok      bool
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, backend := range []*state.PGConcurrencyQueueAdmission{firstReplica, secondReplica} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			leaseID, depth, ok, err := backend.TryAcquireConcurrencyQueueLease(context.Background(), appID, 1, time.Minute)
			results <- result{leaseID: leaseID, depth: depth, ok: ok, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	admitted := 0
	winningLease := ""
	for got := range results {
		if got.err != nil {
			t.Fatalf("concurrent fleet admission: %v", got.err)
		}
		if got.depth != 1 {
			t.Fatalf("observed depth=%d, want 1", got.depth)
		}
		if got.ok {
			admitted++
			winningLease = got.leaseID
		}
	}
	if admitted != 1 || winningLease == "" {
		t.Fatalf("admitted=%d winning lease=%q, want exactly one", admitted, winningLease)
	}

	if err := firstReplica.ReleaseConcurrencyQueueLease(ctx, appID, winningLease); err != nil {
		t.Fatalf("release winning lease: %v", err)
	}
	expiredLease, _, ok, err := firstReplica.TryAcquireConcurrencyQueueLease(ctx, appID, 1, time.Minute)
	if err != nil || !ok {
		t.Fatalf("acquire lease to expire: ok=%v err=%v", ok, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE gateway_concurrency_queue_leases SET expires_at = now() - interval '1 second' WHERE lease_id = $1`, expiredLease); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	replacement, depth, ok, err := secondReplica.TryAcquireConcurrencyQueueLease(ctx, appID, 1, time.Minute)
	if err != nil || !ok || depth != 1 || replacement == "" {
		t.Fatalf("replacement after expiry = lease %q depth %d ok %v err %v", replacement, depth, ok, err)
	}
	if err := secondReplica.ReleaseConcurrencyQueueLease(ctx, appID, replacement); err != nil {
		t.Fatalf("release replacement: %v", err)
	}
}
