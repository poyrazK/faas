package state_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_Jobs_AtomicAccountAndRunCapacityAcrossDispatchers(t *testing.T) {
	store, _, ctx := pgJobsStoreWithPool(t)
	job, run, _ := pgJobsSeed(t, store, ctx, "atomic-capacity")
	nodeID := resolveDefaultLocal(t, ctx, store)
	claim := func(runID string, index int) (string, error) {
		id := uuid.NewString()
		_, err := store.CreateAndClaimJobInstance(ctx, id, job.ID, runID, index,
			"cold_booting", 128, nodeID, id, uuid.NewString(), time.Now().Add(time.Minute), nodeID)
		return id, err
	}
	for index := 0; index < 2; index++ {
		if _, err := claim(run.ID, index); err != nil {
			t.Fatalf("claim run task %d: %v", index, err)
		}
	}
	rejectedID, err := claim(run.ID, 2)
	var quota *state.JobQuotaError
	if !errors.As(err, &quota) || quota.Scope != state.JobQuotaScopeParallelism || quota.Limit != 2 {
		t.Fatalf("third run claim = %v, want parallelism cap 2", err)
	}
	if _, err := store.InstanceByID(ctx, rejectedID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected run instance exists: %v", err)
	}

	other, _, err := store.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, nil, nil, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		id    string
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for index := 0; index < 2; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			id, err := claim(other.ID, index)
			results <- result{id: id, index: index, err: err}
		}(index)
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, rejected := 0, 0
	for result := range results {
		if result.err == nil {
			succeeded++
			continue
		}
		if !errors.As(result.err, &quota) || quota.Scope != state.JobQuotaScopeConcurrent || quota.Limit != 3 {
			t.Fatalf("concurrent claim error = %v, want account cap 3", result.err)
		}
		rejected++
		if _, err := store.InstanceByID(ctx, result.id); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("rejected account instance exists: %v", err)
		}
		task, err := store.JobTaskGet(ctx, other.ID, result.index)
		if err != nil || task.Status != "queued" {
			t.Fatalf("rejected task = %+v, %v, want queued", task, err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent claims: success=%d rejected=%d, want 1 each", succeeded, rejected)
	}
	if live, err := store.JobConcurrentByAccount(ctx, job.AccountID); err != nil || live != 3 {
		t.Fatalf("live account jobs = %d, %v, want 3", live, err)
	}
}

func TestPg_Jobs_RunParallelismOverrideCanExceedTemplateDefault(t *testing.T) {
	store, _, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, store, ctx, "override-capacity") // template default is 4
	if err := store.UpdateAccountPlan(ctx, job.AccountID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	parallelism := 5
	run, _, err := store.JobRunCreate(ctx, job.ID, job.AccountID, "manual", &parallelism, nil, nil, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := resolveDefaultLocal(t, ctx, store)
	for index := 0; index < 5; index++ {
		id := uuid.NewString()
		if _, err := store.CreateAndClaimJobInstance(ctx, id, job.ID, run.ID, index,
			"cold_booting", 128, nodeID, id, uuid.NewString(), time.Now().Add(time.Minute), nodeID); err != nil {
			t.Fatalf("claim override task %d: %v", index, err)
		}
	}
}

func TestPg_Jobs_DeferQueuedPreservesConcurrentClaimAndRetry(t *testing.T) {
	store, _, ctx := pgJobsStoreWithPool(t)
	job, run, _ := pgJobsSeed(t, store, ctx, "defer-capacity")
	deferredUntil := time.Now().Add(2 * time.Minute).UTC()
	if err := store.JobTaskDeferQueued(ctx, run.ID, 0, 1, deferredUntil); err != nil {
		t.Fatalf("defer eligible task: %v", err)
	}
	if err := store.JobTaskDeferQueued(ctx, run.ID, 0, 1, time.Now().Add(time.Second)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("shorten existing backoff = %v, want ErrNotFound", err)
	}
	deferred, err := store.JobTaskGet(ctx, run.ID, 0)
	if err != nil || deferred.Status != "queued" || deferred.NextAttemptAt == nil || deferred.NextAttemptAt.Before(time.Now().Add(time.Minute)) {
		t.Fatalf("deferred task = %+v, %v", deferred, err)
	}

	nodeID, instanceID, lease := resolveDefaultLocal(t, ctx, store), uuid.NewString(), uuid.NewString()
	if _, err := store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, 1,
		"cold_booting", 128, nodeID, instanceID, lease, time.Now().Add(time.Minute), nodeID); err != nil {
		t.Fatal(err)
	}
	if err := store.JobTaskDeferQueued(ctx, run.ID, 1, 1, time.Now().Add(time.Second)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("defer claimed task = %v, want ErrNotFound", err)
	}
	claimed, err := store.JobTaskGet(ctx, run.ID, 1)
	if err != nil || claimed.Status != "claimed" || claimed.InstanceID == nil || *claimed.InstanceID != instanceID || claimed.LeaseToken == nil || *claimed.LeaseToken != lease {
		t.Fatalf("claimed task lost ownership: %+v, %v", claimed, err)
	}
	if err := store.JobTaskMarkTerminal(ctx, run.ID, 1, "failed", 1, "infra", "boot failed", time.Now()); err != nil {
		t.Fatal(err)
	}
	retryAt := time.Now().Add(10 * time.Minute).UTC()
	if err := store.JobTaskRetry(ctx, run.ID, 1, retryAt); err != nil {
		t.Fatal(err)
	}
	if err := store.JobTaskDeferQueued(ctx, run.ID, 1, 1, time.Now().Add(time.Second)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("defer stale attempt = %v, want ErrNotFound", err)
	}
	retried, err := store.JobTaskGet(ctx, run.ID, 1)
	if err != nil || retried.Status != "queued" || retried.Attempt != 2 || retried.NextAttemptAt == nil || retried.NextAttemptAt.Before(time.Now().Add(9*time.Minute)) {
		t.Fatalf("retry backoff changed: %+v, %v", retried, err)
	}
}
