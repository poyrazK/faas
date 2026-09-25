package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_Jobs_ImageFailureSettlesQueuedRun(t *testing.T) {
	store, _, ctx := pgJobsStoreWithPool(t)
	job, run, _ := pgJobsSeed(t, store, ctx, "image-failure-settle")
	if _, err := store.JobUpdate(ctx, job.ID, []string{"/different"}, nil, nil, nil, nil, nil, nil, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("update with active run = %v, want ErrConflict", err)
	}
	if deleted, active, err := store.JobSoftDelete(ctx, job.ID); err != nil || deleted || !active {
		t.Fatalf("delete with queued run = (%v, %v, %v)", deleted, active, err)
	}
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "failed", "", "", "registry image not found"); err != nil {
		t.Fatal(err)
	}
	settled, err := store.JobRunGetByID(ctx, run.ID)
	if err != nil || settled.AggregateStatus != "failed" || settled.TasksFailed != 3 || settled.FinishedAt == nil {
		t.Fatalf("settled run = %+v, %v", settled, err)
	}
	if _, _, err := store.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, nil, nil, nil, 1); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("run on failed image = %v, want ErrConflict", err)
	}
}

func TestPg_Jobs_ReapClaimedRetriesAndDeadLetters(t *testing.T) {
	store, pool, ctx := pgJobsStoreWithPool(t)
	job, run, _ := pgJobsSeed(t, store, ctx, "reap-retry")
	expired := time.Now().Add(-time.Hour)
	cutoff := time.Now().Add(-time.Minute)
	instanceID := pgJobsCreateJobTaskInstance(t, pool, ctx, job.AccountID, job.ID)
	lease := uuid.NewString()
	if err := store.JobTaskMarkClaimed(ctx, run.ID, 0, instanceID, lease, expired, resolveDefaultLocal(t, ctx, store)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobTaskReapClaimed(ctx, run.ID, 0, lease, expired.Add(-time.Second), 1, time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("premature reap = %v, want ErrNotFound", err)
	}
	retry, err := store.JobTaskReapClaimed(ctx, run.ID, 0, lease, cutoff, 1, time.Now())
	if err != nil || !retry {
		t.Fatalf("first reap = (%v, %v)", retry, err)
	}
	task, err := store.JobTaskGet(ctx, run.ID, 0)
	if err != nil || task.Status != "queued" || task.Attempt != 2 {
		t.Fatalf("retried task = %+v, %v", task, err)
	}
	instanceID = pgJobsCreateJobTaskInstance(t, pool, ctx, job.AccountID, job.ID)
	lease = uuid.NewString()
	if err := store.JobTaskMarkClaimed(ctx, run.ID, 0, instanceID, lease, expired, resolveDefaultLocal(t, ctx, store)); err != nil {
		t.Fatal(err)
	}
	retry, err = store.JobTaskReapClaimed(ctx, run.ID, 0, lease, cutoff, 1, time.Now())
	if err != nil || retry {
		t.Fatalf("second reap = (%v, %v)", retry, err)
	}
	settled, err := store.JobRunRecompute(ctx, run.ID)
	if err != nil || settled.TasksFailed != 1 || settled.DeadLetterCount != 1 {
		t.Fatalf("run after exhausted retry = %+v, %v", settled, err)
	}
}
