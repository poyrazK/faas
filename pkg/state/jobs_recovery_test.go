package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestJobImageFailureSettlesQueuedRuns(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, err := store.JobCreate(ctx, "account", "broken-image", "app", "example.invalid/image:v1", []string{"/job"}, 128, 30, 1, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, nil, nil, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobUpdate(ctx, job.ID, []string{"/other"}, nil, nil, nil, nil, nil, nil, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("update with active run = %v, want ErrConflict", err)
	}
	if deleted, active, err := store.JobSoftDelete(ctx, job.ID); err != nil || deleted || !active {
		t.Fatalf("delete with queued run = (%v, %v, %v), want active-work conflict", deleted, active, err)
	}
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "failed", "", "", "registry image not found"); err != nil {
		t.Fatal(err)
	}
	settled, err := store.JobRunGetByID(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.AggregateStatus != "failed" || settled.TasksFailed != 2 || settled.FinishedAt == nil {
		t.Fatalf("failed image left run unsettled: %+v", settled)
	}
	for i := 0; i < 2; i++ {
		task, err := store.JobTaskGet(ctx, run.ID, i)
		if err != nil || task.Status != "failed" || task.ErrorMessage == nil || *task.ErrorMessage != "registry image not found" {
			t.Fatalf("task %d = %+v, %v", i, task, err)
		}
	}
	if _, _, err := store.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, nil, nil, nil, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("create run on failed image = %v, want ErrConflict", err)
	}
	newImage := "example.invalid/image:v2"
	if _, err := store.JobUpdate(ctx, job.ID, nil, &newImage, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("update after run settled: %v", err)
	}
}

func TestJobTaskReapClaimedRetriesThenDeadLetters(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, err := store.JobCreate(ctx, "account", "reap", "app", "example.invalid/image:v1", []string{"/job"}, 128, 30, 1, 1, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Minute)
	cutoff := time.Now().Add(-30 * time.Second)
	if err := store.JobTaskMarkClaimed(ctx, run.ID, 0, "instance-1", "lease-1", expired, "node-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobTaskReapClaimed(ctx, run.ID, 0, "lease-1", expired.Add(-time.Second), 1, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("premature reap = %v, want ErrNotFound", err)
	}
	retry, err := store.JobTaskReapClaimed(ctx, run.ID, 0, "lease-1", cutoff, 1, time.Now())
	if err != nil || !retry {
		t.Fatalf("first reap = (%v, %v), want retry", retry, err)
	}
	task, _ := store.JobTaskGet(ctx, run.ID, 0)
	if task.Status != "queued" || task.Attempt != 2 {
		t.Fatalf("retried task = %+v", task)
	}
	if err := store.JobTaskMarkClaimed(ctx, run.ID, 0, "instance-2", "lease-2", expired, "node-1"); err != nil {
		t.Fatal(err)
	}
	retry, err = store.JobTaskReapClaimed(ctx, run.ID, 0, "lease-2", cutoff, 1, time.Now())
	if err != nil || retry {
		t.Fatalf("second reap = (%v, %v), want terminal", retry, err)
	}
	settled, err := store.JobRunRecompute(ctx, run.ID)
	if err != nil || settled.AggregateStatus != "dead_letter" || settled.TasksFailed != 1 || settled.DeadLetterCount != 1 {
		t.Fatalf("settled run = %+v, %v", settled, err)
	}
}

func TestJobMaterializationRetryExhaustionSettlesRun(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, err := store.JobCreate(ctx, "account", "retry-exhausted-image", "app", "example.invalid/image:v1", []string{"/job"}, 128, 30, 1, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobClaimImageMaterialization(ctx, job.ID, "worker-a", time.Minute); err != nil {
		t.Fatal(err)
	}
	failed, err := store.JobRecordImageMaterializationFailure(ctx, job.ID, job.ImageRef, "worker-a", "registry timeout", time.Now(), 1)
	if err != nil || failed.ImageMaterializationStatus != "failed" {
		t.Fatalf("materialization failure = %+v, %v", failed, err)
	}
	settled, err := store.JobRunGetByID(ctx, run.ID)
	if err != nil || settled.AggregateStatus != "failed" || settled.TasksFailed != 1 {
		t.Fatalf("run after retry exhaustion = %+v, %v", settled, err)
	}
}
