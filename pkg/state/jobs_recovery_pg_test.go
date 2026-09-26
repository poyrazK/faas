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

func TestPg_Jobs_ImagePublicationIsClaimFenced(t *testing.T) {
	store, pool, ctx := pgJobsStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "pg-image-publish-"+uuid.NewString()+"@example.com", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	job, err := store.JobCreate(ctx, acct.ID, "image-publish", "batch", "registry.example/worker:latest", []string{"/bin/worker"}, 256, 60, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	stale, err := store.JobClaimImageMaterialization(ctx, job.ID, "claim-a", time.Minute)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:unfenced", "jobs/"+job.ID+".ext4", ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unfenced publication = %v, want ErrConflict", err)
	}
	if _, err := pool.Exec(ctx, `update jobs set image_materialization_lease_until = now() - interval '1 second' where id = $1::uuid`, job.ID); err != nil {
		t.Fatalf("expire first lease: %v", err)
	}
	current, err := store.JobClaimImageMaterialization(ctx, job.ID, "claim-b", time.Minute)
	if err != nil || current.ImageMaterializationAttempts != stale.ImageMaterializationAttempts+1 {
		t.Fatalf("second claim = %+v, %v; want incremented attempt", current, err)
	}
	if _, err := store.JobPublishImageMaterialization(ctx, job.ID, job.ImageRef, "claim-a", stale.ImageMaterializationAttempts,
		"sha256:stale", "jobs/"+job.ID+"__01234567-89ab-cdef-0123-456789abcdef.ext4"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale publication = %v, want ErrConflict", err)
	}
	winner, err := store.JobPublishImageMaterialization(ctx, job.ID, job.ImageRef, "claim-b", current.ImageMaterializationAttempts,
		"sha256:current", "jobs/"+job.ID+"__11234567-89ab-cdef-0123-456789abcdef.ext4")
	if err != nil {
		t.Fatalf("current publication: %v", err)
	}
	if winner.ImageMaterializationStatus != "ready" || winner.ImageStorageKey != "jobs/"+job.ID+"__11234567-89ab-cdef-0123-456789abcdef.ext4" {
		t.Fatalf("published job = %+v, want current attempt ready", winner)
	}
}

func TestPg_Jobs_ImageMaterializationLeaseRenewalIsFenced(t *testing.T) {
	store, pool, ctx := pgJobsStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "pg-image-renew-"+uuid.NewString()+"@example.com", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	job, err := store.JobCreate(ctx, acct.ID, "image-renew", "batch", "registry.example/worker:latest", []string{"/bin/worker"}, 256, 60, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	first, err := store.JobClaimImageMaterialization(ctx, job.ID, "claim-a", time.Minute)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.JobRenewImageMaterializationLease(ctx, job.ID, job.ImageRef, "other-owner", first.ImageMaterializationAttempts, time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong-owner renewal = %v, want ErrConflict", err)
	}
	if err := store.JobRenewImageMaterializationLease(ctx, job.ID, job.ImageRef, "claim-a", first.ImageMaterializationAttempts, time.Minute); err != nil {
		t.Fatalf("current lease renewal: %v", err)
	}
	if _, err := store.JobClaimImageMaterialization(ctx, job.ID, "claim-b", time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim after renewal = %v, want ErrNotFound", err)
	}
	if _, err := pool.Exec(ctx, `update jobs set image_materialization_lease_until = now() - interval '1 second' where id = $1::uuid`, job.ID); err != nil {
		t.Fatalf("expire first lease: %v", err)
	}
	if err := store.JobRenewImageMaterializationLease(ctx, job.ID, job.ImageRef, "claim-a", first.ImageMaterializationAttempts, time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired lease renewal = %v, want ErrConflict", err)
	}
	second, err := store.JobClaimImageMaterialization(ctx, job.ID, "claim-b", time.Minute)
	if err != nil || second.ImageMaterializationAttempts != first.ImageMaterializationAttempts+1 {
		t.Fatalf("second claim = %+v, %v", second, err)
	}
	if err := store.JobRenewImageMaterializationLease(ctx, job.ID, job.ImageRef, "claim-a", first.ImageMaterializationAttempts, time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale-attempt renewal = %v, want ErrConflict", err)
	}
	if err := store.JobRenewImageMaterializationLease(ctx, job.ID, job.ImageRef, "claim-b", second.ImageMaterializationAttempts, time.Minute); err != nil {
		t.Fatalf("replacement lease renewal: %v", err)
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
