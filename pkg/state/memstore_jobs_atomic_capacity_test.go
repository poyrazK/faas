package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreJobsAtomicAccountAndRunCapacity(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, run, _ := newJobAndRun(t, store, "acct-capacity", "capacity")
	claim := func(runID string, index int) (string, error) {
		id := newUUIDString()
		_, err := store.CreateAndClaimJobInstance(ctx, id, job.ID, runID, index,
			"cold_booting", 128, DefaultLocalNodeName, id, newUUIDString(), time.Now().Add(time.Minute), DefaultLocalNodeName)
		return id, err
	}
	for index := 0; index < 2; index++ {
		if _, err := claim(run.ID, index); err != nil {
			t.Fatalf("claim run task %d: %v", index, err)
		}
	}
	rejectedID, err := claim(run.ID, 2)
	var quota *JobQuotaError
	if !errors.As(err, &quota) || quota.Scope != JobQuotaScopeParallelism || quota.Limit != 2 {
		t.Fatalf("third run claim = %v, want parallelism cap 2", err)
	}
	if _, err := store.InstanceByID(ctx, rejectedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected run instance exists: %v", err)
	}

	other, _, err := store.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, nil, nil, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claim(other.ID, 0); err != nil {
		t.Fatalf("claim third account slot: %v", err)
	}
	rejectedID, err = claim(other.ID, 1)
	if !errors.As(err, &quota) || quota.Scope != JobQuotaScopeConcurrent || quota.Limit != 3 {
		t.Fatalf("fourth account claim = %v, want account cap 3", err)
	}
	if _, err := store.InstanceByID(ctx, rejectedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected account instance exists: %v", err)
	}
	queued, err := store.JobTaskGet(ctx, other.ID, 1)
	if err != nil || queued.Status != "queued" {
		t.Fatalf("rejected task = %+v, %v, want queued", queued, err)
	}
}

func TestMemStoreJobsRunParallelismOverrideCanExceedTemplateDefault(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "job-override@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	job, _, _ := newJobAndRun(t, store, account.ID, "override") // template default is 4
	parallelism := 5
	run, _, err := store.JobRunCreate(ctx, job.ID, account.ID, "manual", &parallelism, nil, nil, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		id := newUUIDString()
		if _, err := store.CreateAndClaimJobInstance(ctx, id, job.ID, run.ID, index,
			"cold_booting", 128, DefaultLocalNodeName, id, newUUIDString(), time.Now().Add(time.Minute), DefaultLocalNodeName); err != nil {
			t.Fatalf("claim override task %d: %v", index, err)
		}
	}
}

func TestMemStoreJobsDeferQueuedPreservesConcurrentClaimAndRetry(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, run, _ := newJobAndRun(t, store, "acct-defer", "defer")
	deferredUntil := time.Now().Add(2 * time.Minute).UTC()
	if err := store.JobTaskDeferQueued(ctx, run.ID, 0, 1, deferredUntil); err != nil {
		t.Fatalf("defer eligible task: %v", err)
	}
	deferred, err := store.JobTaskGet(ctx, run.ID, 0)
	if err != nil || deferred.Status != "queued" || deferred.Attempt != 1 || deferred.NextAttemptAt == nil || !deferred.NextAttemptAt.Equal(deferredUntil) {
		t.Fatalf("deferred task = %+v, %v", deferred, err)
	}
	if err := store.JobTaskDeferQueued(ctx, run.ID, 0, 1, time.Now().Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("shorten existing backoff = %v, want ErrNotFound", err)
	}

	instanceID, lease := newUUIDString(), newUUIDString()
	if _, err := store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, 1,
		"cold_booting", 128, DefaultLocalNodeName, instanceID, lease, time.Now().Add(time.Minute), DefaultLocalNodeName); err != nil {
		t.Fatal(err)
	}
	if err := store.JobTaskDeferQueued(ctx, run.ID, 1, 1, time.Now().Add(time.Second)); !errors.Is(err, ErrNotFound) {
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
	if err := store.JobTaskDeferQueued(ctx, run.ID, 1, 1, time.Now().Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("defer stale attempt = %v, want ErrNotFound", err)
	}
	retried, err := store.JobTaskGet(ctx, run.ID, 1)
	if err != nil || retried.Status != "queued" || retried.Attempt != 2 || retried.NextAttemptAt == nil || !retried.NextAttemptAt.Equal(retryAt) {
		t.Fatalf("retry backoff changed: %+v, %v", retried, err)
	}
}
