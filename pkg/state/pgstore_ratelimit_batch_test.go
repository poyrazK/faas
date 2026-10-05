// adr: 104 — batched central rate-limit consumes.
package state_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPGRateLimitBackendConsumeTokensGrantsUpToBalance pins the batched
// consume the gateway uses for requests that queue behind an in-flight
// consult: a missing row is created with the grant taken, a partial balance
// is granted in full, and an empty bucket grants nothing.
func TestPGRateLimitBackendConsumeTokensGrantsUpToBalance(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	backend := state.NewPGRateLimitBackend(pool)
	subjectID := uuid.NewString()

	steps := []struct {
		n                      int
		wantGranted, wantAfter int
	}{
		{n: 3, wantGranted: 3, wantAfter: 2}, // creates the row
		{n: 5, wantGranted: 2, wantAfter: 0}, // partial grant
		{n: 2, wantGranted: 0, wantAfter: 0}, // empty bucket
	}
	for i, step := range steps {
		granted, remaining, err := backend.ConsumeTokens(ctx, "app", subjectID, "hobby", 0.1, 5, step.n)
		if err != nil || granted != step.wantGranted || remaining != step.wantAfter {
			t.Fatalf("step %d ConsumeTokens(n=%d) = (%d, %d, %v), want (%d, %d, nil)",
				i, step.n, granted, remaining, err, step.wantGranted, step.wantAfter)
		}
	}
	if _, ok, err := backend.ConsumeToken(ctx, "app", subjectID, "hobby", 0.1, 5); err != nil || ok {
		t.Fatalf("single consume after the batch drained the bucket = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	if granted, _, err := backend.ConsumeTokens(ctx, "app", subjectID, "hobby", 0, 5, 3); err != nil || granted != 0 {
		t.Fatalf("invalid policy = (%d, %v), want (0, nil)", granted, err)
	}
}

// TestPGRateLimitBackendConsumeTokensKeepsFractionalRefill checks the batched
// statement advances last_refill like ConsumeToken: only by whole tokens.
func TestPGRateLimitBackendConsumeTokensKeepsFractionalRefill(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	backend := state.NewPGRateLimitBackend(pool)
	subjectID := uuid.NewString()

	if granted, _, err := backend.ConsumeTokens(ctx, "app", subjectID, "hobby", 0.1, 2, 2); err != nil || granted != 2 {
		t.Fatalf("drain = (%d, %v), want (2, nil)", granted, err)
	}
	if _, err := pool.Exec(ctx, `
		update pg_ratelimit_counters
		   set last_refill = now() - interval '15 seconds'
		 where scope = 'app' and subject_id = $1 and plan = 'hobby'`, subjectID); err != nil {
		t.Fatalf("age rate-limit row: %v", err)
	}
	// 15 s at 0.1 rps refills 1.5 tokens: one whole token is granted and the
	// remaining 5 s of refill time is kept.
	if granted, remaining, err := backend.ConsumeTokens(ctx, "app", subjectID, "hobby", 0.1, 2, 3); err != nil || granted != 1 || remaining != 0 {
		t.Fatalf("refilled batch = (%d, %d, %v), want (1, 0, nil)", granted, remaining, err)
	}
	var residualSeconds float64
	if err := pool.QueryRow(ctx, `
		select extract(epoch from (now() - last_refill))
		  from pg_ratelimit_counters
		 where scope = 'app' and subject_id = $1 and plan = 'hobby'`, subjectID).Scan(&residualSeconds); err != nil {
		t.Fatalf("read residual refill time: %v", err)
	}
	if residualSeconds < 2 || residualSeconds > 8 {
		t.Fatalf("residual refill time=%0.3fs, want about 5s", residualSeconds)
	}
}

// TestPGRateLimitBackendConsumeTokensSerializesReplicas runs batches from
// several replicas at once, including the race to create the row, and checks
// the counter never grants more than its burst.
func TestPGRateLimitBackendConsumeTokensSerializesReplicas(t *testing.T) {
	_, pool, _ := pgStoreWithPool(t)
	backend := state.NewPGRateLimitBackend(pool)
	subjectID := uuid.NewString()

	const replicas, perBatch, burst = 6, 3, 10
	start := make(chan struct{})
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
		errs    []error
	)
	for range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, _, err := backend.ConsumeTokens(context.Background(), "app", subjectID, "hobby", 0.001, burst, perBatch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			granted += got
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent batches: %v", errs)
	}
	if granted != burst {
		t.Fatalf("granted %d across %d replicas asking for %d each, want exactly the burst %d", granted, replicas, perBatch, burst)
	}
}
