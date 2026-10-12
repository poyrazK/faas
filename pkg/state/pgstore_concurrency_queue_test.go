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

// Production-us hunt #9 (H9-76): the acquire now runs as one server-side call.
// Many replicas racing for one app's permits must still admit exactly the
// cap, each with a distinct depth, and leave exactly that many lease rows.
func TestPGConcurrencyQueueAdmissionCapHoldsUnderContention(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, store, ctx, "-queue-contend", "queue-contend")
	const contenders, limit = 24, 5

	start := make(chan struct{})
	var mu sync.Mutex
	depths := map[int]bool{}
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			backend := state.NewPGConcurrencyQueueAdmission(pool)
			leaseID, depth, ok, err := backend.TryAcquireConcurrencyQueueLease(context.Background(), appID, limit, time.Minute)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				t.Errorf("acquire: %v", err)
			case ok && leaseID == "":
				t.Errorf("admitted without a lease id")
			case ok && depths[depth]:
				t.Errorf("two admissions observed depth %d", depth)
			case ok:
				depths[depth] = true
			case depth != limit:
				t.Errorf("refused at depth %d, want %d", depth, limit)
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(depths) != limit {
		t.Fatalf("admitted %d leases (depths %v), want %d", len(depths), depths, limit)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gateway_concurrency_queue_leases WHERE app_id = $1`, appID).Scan(&rows); err != nil {
		t.Fatalf("count leases: %v", err)
	}
	if rows != limit {
		t.Fatalf("lease rows = %d, want %d", rows, limit)
	}
}
