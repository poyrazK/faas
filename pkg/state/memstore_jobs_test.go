// memstore_jobs_test.go exercises the JobStore sub-interface against
// the in-memory backend. The test mirrors the parity pattern
// memstore_cron_runs_test.go uses for the cron surface — same
// shape (NewMemStore → CRUD → assert), so a test failure here
// signals either a memstore-jobs regression OR a JobStore signature
// drift that breaks the pgstore-jobs test path symmetrically.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestMemStoreJobs is the end-to-end happy-path walk:
// JobCreate → JobGetByName → JobRunCreate(3) → JobTaskList(3) →
// JobTaskClaimBatch(2) → JobTaskMarkTerminal(succeeded) → JobTaskRetry →
// JobSoftDelete → JobConcurrentByAccount.
//
// Each step asserts one observable invariant so a regression points
// at the broken method directly.
func TestMemStoreJobs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()

	// 1. JobCreate round-trip. Image ref + env overrides pass through
	//    as-is so a test failure on retrieval is unambiguous.
	created, err := ms.JobCreate(ctx,
		"account-uuid-1",
		"nightly-export",
		"app",
		"oci://registry.example/export@sha256:deadbeef",
		[]string{"/bin/sh", "-c", "echo hello"},
		256, 60, 4, 3,
		json.RawMessage(`{"DATABASE_URL":"postgres://example/db"}`),
	)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	if created.Name != "nightly-export" {
		t.Fatalf("JobCreate.Name = %q, want nightly-export", created.Name)
	}
	if created.Status != "active" {
		t.Fatalf("JobCreate.Status = %q, want active", created.Status)
	}
	if len(created.Command) != 3 {
		t.Fatalf("JobCreate.Command len = %d, want 3", len(created.Command))
	}
	if _, err := ms.JobSetImageMaterialization(ctx, created.ID, created.ImageRef,
		"ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+created.ID+".ext4", ""); err != nil {
		t.Fatalf("JobSetImageMaterialization: %v", err)
	}
	if !strings.Contains(string(created.EnvOverrides), "DATABASE_URL") {
		t.Fatalf("JobCreate.EnvOverrides = %s, missing DATABASE_URL", string(created.EnvOverrides))
	}

	// 2. JobGetByName returns the same row. This is the read-back
	//    path the dashboard's GET /v1/jobs/{name} handler uses.
	got, err := ms.JobGetByName(ctx, created.AccountID, "nightly-export")
	if err != nil {
		t.Fatalf("JobGetByName: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("JobGetByName.ID = %q, want %q", got.ID, created.ID)
	}

	// 3. JobRunCreate fans out three tasks. Asserting the run shape
	//    AND the fan-out slice in one place keeps the per-task
	//    invariants (status='queued', attempt=1) under one assertion.
	parallelism := 2
	run, fanned, err := ms.JobRunCreate(ctx,
		created.ID, created.AccountID, "manual",
		&parallelism, nil, nil,
		json.RawMessage(`{}`), 3,
	)
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	if run.Tasks != 3 {
		t.Fatalf("JobRunCreate.Tasks = %d, want 3", run.Tasks)
	}
	if run.AggregateStatus != "queued" {
		t.Fatalf("JobRunCreate.AggregateStatus = %q, want queued", run.AggregateStatus)
	}
	if len(fanned) != 3 {
		t.Fatalf("JobRunCreate fan-out len = %d, want 3", len(fanned))
	}
	for i, ft := range fanned {
		if ft.TaskIndex != i {
			t.Fatalf("fanned[%d].TaskIndex = %d, want %d", i, ft.TaskIndex, i)
		}
		if ft.Status != "queued" {
			t.Fatalf("fanned[%d].Status = %q, want queued", i, ft.Status)
		}
		if ft.Attempt != 1 {
			t.Fatalf("fanned[%d].Attempt = %d, want 1", i, ft.Attempt)
		}
	}

	// 4. JobTaskList mirrors the fanned slice.
	tasks, err := ms.JobTaskList(ctx, run.ID, 100, 0)
	if err != nil {
		t.Fatalf("JobTaskList: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("JobTaskList len = %d, want 3", len(tasks))
	}

	// 5. JobTaskClaimBatch picks up to `limit` queued tasks. We
	//    claim 2 of 3 so the test can later assert the third stays
	//    queued through MarkTerminal+Retry on task 0.
	claimed, err := ms.JobTaskClaimBatch(ctx, 2)
	if err != nil {
		t.Fatalf("JobTaskClaimBatch: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("JobTaskClaimBatch len = %d, want 2", len(claimed))
	}

	// 6. JobTaskMarkTerminal transitions task 0 to 'failed'.
	//    Asserting via JobTaskGet (not the slice from step 5) ensures
	//    the transition was actually persisted, not just returned
	//    by the claim call.
	now := time.Now().UTC()
	if err := ms.JobTaskMarkTerminal(ctx, run.ID, 0, "failed", 1, "user_error", "boom", now); err != nil {
		t.Fatalf("JobTaskMarkTerminal: %v", err)
	}
	got0, err := ms.JobTaskGet(ctx, run.ID, 0)
	if err != nil {
		t.Fatalf("JobTaskGet(0): %v", err)
	}
	if got0.Status != "failed" {
		t.Fatalf("JobTaskGet(0).Status = %q, want failed", got0.Status)
	}
	if got0.FinishedAt == nil {
		t.Fatalf("JobTaskGet(0).FinishedAt is nil; MarkTerminal should stamp it")
	}

	// 7. JobTaskRetry reverses the terminal transition back to queued
	//    and increments the attempt counter.
	next := time.Now().Add(time.Minute).UTC()
	if err := ms.JobTaskRetry(ctx, run.ID, 0, next); err != nil {
		t.Fatalf("JobTaskRetry: %v", err)
	}
	got0Retry, err := ms.JobTaskGet(ctx, run.ID, 0)
	if err != nil {
		t.Fatalf("JobTaskGet(0 retry): %v", err)
	}
	if got0Retry.Status != "queued" {
		t.Fatalf("JobTaskGet(0 retry).Status = %q, want queued", got0Retry.Status)
	}
	if got0Retry.Attempt != 2 {
		t.Fatalf("JobTaskGet(0 retry).Attempt = %d, want 2", got0Retry.Attempt)
	}
	if got0Retry.NextAttemptAt == nil {
		t.Fatalf("JobTaskGet(0 retry).NextAttemptAt is nil; Retry should stamp it")
	}

	// 8. JobSoftDelete is the no-live-instance path. After we cancel
	//    the remaining claimed/queued tasks, the soft-delete should
	//    succeed and return (true, false, nil).
	//    First, mark task 0 as cancelled (terminal) so the live-task
	//    detection walk finds nothing for the job.
	if err := ms.JobTaskCancel(ctx, run.ID, 0); err != nil {
		t.Fatalf("JobTaskCancel(0): %v", err)
	}
	if err := ms.JobTaskCancel(ctx, run.ID, 1); err != nil {
		t.Fatalf("JobTaskCancel(1): %v", err)
	}
	if err := ms.JobTaskCancel(ctx, run.ID, 2); err != nil {
		t.Fatalf("JobTaskCancel(2): %v", err)
	}
	deleted, hasLive, err := ms.JobSoftDelete(ctx, created.ID)
	if err != nil {
		t.Fatalf("JobSoftDelete: %v", err)
	}
	if !deleted {
		t.Fatalf("JobSoftDelete.deleted = false, want true (no live tasks remain)")
	}
	if hasLive {
		t.Fatalf("JobSoftDelete.hasLiveInstances = true, want false")
	}

	// 9. Idempotent re-call returns (false, false, nil) — the row
	//    is already soft-deleted.
	deleted2, hasLive2, err := ms.JobSoftDelete(ctx, created.ID)
	if err != nil {
		t.Fatalf("JobSoftDelete (re-call): %v", err)
	}
	if deleted2 {
		t.Fatalf("JobSoftDelete.deleted (re-call) = true, want false (idempotent)")
	}
	if hasLive2 {
		t.Fatalf("JobSoftDelete.hasLiveInstances (re-call) = true, want false")
	}

	// 10. JobConcurrentByAccount counts only live (queued + claimed)
	//     tasks. With every task terminal, the count is 0.
	concurrent, err := ms.JobConcurrentByAccount(ctx, created.AccountID)
	if err != nil {
		t.Fatalf("JobConcurrentByAccount: %v", err)
	}
	if concurrent != 0 {
		t.Fatalf("JobConcurrentByAccount = %d, want 0 (all tasks terminal)", concurrent)
	}

	// 11. JobGetByID on a soft-deleted row returns ErrNotFound —
	//     the customer-facing CRUD invisibility rule.
	_, err = ms.JobGetByID(ctx, created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobGetByID on soft-deleted row: err = %v, want ErrNotFound", err)
	}
}
