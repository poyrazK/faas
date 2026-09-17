package state_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGRateLimitBackendPreservesFractionalRefillTime(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	backend := state.NewPGRateLimitBackend(pool)
	subjectID := uuid.NewString()

	if remaining, ok, err := backend.ConsumeToken(ctx, "app", subjectID, "hobby", 0.1, 2); err != nil || !ok || remaining != 1 {
		t.Fatalf("first consume=(%d,%v,%v), want (1,true,nil)", remaining, ok, err)
	}
	if remaining, ok, err := backend.ConsumeToken(ctx, "app", subjectID, "hobby", 0.1, 2); err != nil || !ok || remaining != 0 {
		t.Fatalf("second consume=(%d,%v,%v), want (0,true,nil)", remaining, ok, err)
	}
	if _, ok, err := backend.ConsumeToken(ctx, "app", subjectID, "hobby", 0.1, 2); err != nil || ok {
		t.Fatalf("empty consume=(ok=%v,err=%v), want (false,nil)", ok, err)
	}

	if _, err := pool.Exec(ctx, `
		update pg_ratelimit_counters
		   set last_refill = now() - interval '15 seconds'
		 where scope = 'app' and subject_id = $1 and plan = 'hobby'`, subjectID); err != nil {
		t.Fatalf("age rate-limit row: %v", err)
	}
	if remaining, ok, err := backend.ConsumeToken(ctx, "app", subjectID, "hobby", 0.1, 2); err != nil || !ok || remaining != 0 {
		t.Fatalf("refilled consume=(%d,%v,%v), want (0,true,nil)", remaining, ok, err)
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
	if _, ok, err := backend.ConsumeToken(ctx, "app", subjectID, "hobby", 0.1, 2); err != nil || ok {
		t.Fatalf("consume before residual reaches one token=(ok=%v,err=%v), want (false,nil)", ok, err)
	}
}

func TestPGRateLimitBackendSerializesReplicaBurst(t *testing.T) {
	_, pool, _ := pgStoreWithPool(t)
	backend := state.NewPGRateLimitBackend(pool)
	subjectID := uuid.NewString()

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, ok, err := backend.ConsumeToken(context.Background(), "rule", subjectID, "hobby", 1, 1)
			if err != nil {
				errs <- err
				return
			}
			results <- ok
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent consume: %v", err)
	}
	admitted := 0
	for ok := range results {
		if ok {
			admitted++
		}
	}
	if admitted != 1 {
		t.Fatalf("admitted=%d across two replicas, want 1", admitted)
	}
}

func TestPGQueueTriggerOwnsLegacyInvocationDrain(t *testing.T) {
	store, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx, "queue-owner", "queue-owner")
	limits := api.MustLimitsFor(api.PlanPro)
	trigger, err := store.CreateTriggerIfUnderQuota(
		ctx, appID, "queue", "jobs", "queue", true,
		[]byte(`{"mode":"queue"}`), 10, 1000, 3, 1<<20, "commit", limits,
	)
	if err != nil {
		t.Fatalf("CreateTriggerIfUnderQuota: %v", err)
	}
	if !trigger.Source.Valid || trigger.Source.String != "queue" {
		t.Fatalf("trigger source=%+v, want queue", trigger.Source)
	}
	if _, err := store.CreateTriggerIfUnderQuota(
		ctx, appID, "queue", "jobs-duplicate", "queue", true,
		[]byte(`{"mode":"queue"}`), 10, 1000, 3, 1<<20, "commit", limits,
	); err == nil {
		t.Fatal("second enabled queue owner succeeded, want unique ownership error")
	}

	invocation, err := store.EnqueueInvocation(ctx, state.Invocation{
		AccountID: accountID,
		AppID:     appID,
		Source:    state.InvocationQueue,
		Payload:   json.RawMessage(`{"job":"one"}`),
		DueAt:     time.Now().Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	due, err := store.ListDueInvocations(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("ListDueInvocations with owner: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("legacy drain returned owned invocation: %+v", due)
	}

	disabled := false
	if _, err := store.UpdateTrigger(
		ctx, trigger.ID.String(), &disabled, nil, nil, nil, nil, nil, nil, nil, nil,
	); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}
	due, err = store.ListDueInvocations(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("ListDueInvocations without owner: %v", err)
	}
	if len(due) != 1 || due[0].ID != invocation.ID {
		t.Fatalf("legacy drain rows=%+v, want invocation %s", due, invocation.ID)
	}
}
