package state_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGPreAuthFailuresCheckRecordAndRefill(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	backend := state.NewPGRateLimitBackend(pool)
	subjectID := uuid.NewString()
	const rate = 0.1

	check := func(wantAllowed bool) {
		t.Helper()
		allowed, _, err := backend.CheckPreAuthFailure(ctx, subjectID, "hobby", rate, 2)
		if err != nil || allowed != wantAllowed {
			t.Fatalf("check=(%t,%v), want allowed=%t", allowed, err, wantAllowed)
		}
	}
	check(true) // A missing row starts with a full burst.
	for range 2 {
		if err := backend.RecordPreAuthFailure(ctx, subjectID, "hobby", rate, 2); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	check(false)
	if _, err := pool.Exec(ctx, `UPDATE pg_preauth_failure_counters SET last_refill = now() - interval '15 seconds' WHERE subject_id = $1`, subjectID); err != nil {
		t.Fatalf("age counter: %v", err)
	}
	check(true)
	if err := backend.RecordPreAuthFailure(ctx, subjectID, "hobby", rate, 2); err != nil {
		t.Fatalf("record refilled failure: %v", err)
	}
	check(false)
	var residualSeconds float64
	if err := pool.QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM (now() - last_refill)) FROM pg_preauth_failure_counters WHERE subject_id = $1`, subjectID).Scan(&residualSeconds); err != nil {
		t.Fatalf("read refill time: %v", err)
	}
	if residualSeconds < 2 || residualSeconds > 8 {
		t.Fatalf("residual refill time=%0.3fs, want about 5s", residualSeconds)
	}
}

func TestPGPreAuthFailuresConcurrentResponsesPreserveBoundedDebt(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	backend := state.NewPGRateLimitBackend(pool)
	subjectID := uuid.NewString()
	const responses = 4
	start := make(chan struct{})
	results := make(chan error, responses)
	var wg sync.WaitGroup
	for range responses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- backend.RecordPreAuthFailure(context.Background(), subjectID, "hobby", 1.0/60, 1)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent record: %v", err)
		}
	}
	var tokens int
	if err := pool.QueryRow(ctx, `SELECT tokens FROM pg_preauth_failure_counters WHERE subject_id = $1`, subjectID).Scan(&tokens); err != nil || tokens != -1 {
		t.Fatalf("tokens=(%d,%v), want capped debt -1", tokens, err)
	}
	allowed, retryAfter, err := backend.CheckPreAuthFailure(ctx, subjectID, "hobby", 1.0/60, 1)
	if err != nil || allowed || retryAfter < 119 || retryAfter > 120 {
		t.Fatalf("debt check=(%t,%d,%v), want blocked for about 120s", allowed, retryAfter, err)
	}
}

func TestPGPreAuthFailuresPrunesOnlyFullyRecoveredIdleCounters(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	backend := state.NewPGRateLimitBackend(pool)
	staleID, freshID := uuid.NewString(), uuid.NewString()
	for _, id := range []string{staleID, freshID} {
		if err := backend.RecordPreAuthFailure(ctx, id, "hobby", 1.0/60, 1); err != nil {
			t.Fatalf("seed counter: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE pg_preauth_failure_counters SET last_refill = now() - interval '8 days' WHERE subject_id = $1`, staleID); err != nil {
		t.Fatalf("age counter: %v", err)
	}
	if removed, err := backend.PrunePreAuthFailureCounters(ctx); err != nil || removed != 1 {
		t.Fatalf("prune=(%d,%v), want (1,nil)", removed, err)
	}
	for _, row := range []struct {
		id   string
		want int
	}{{staleID, 0}, {freshID, 1}} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_preauth_failure_counters WHERE subject_id = $1`, row.id).Scan(&count); err != nil || count != row.want {
			t.Fatalf("counter count=(%d,%v), want %d", count, err, row.want)
		}
	}
}
