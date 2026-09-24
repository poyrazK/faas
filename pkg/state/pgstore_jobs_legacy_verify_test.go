package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_Jobs_LegacyArtifactVerification(t *testing.T) {
	store, pool, ctx := pgJobsStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "pg-legacy-artifact@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	const ref = "apps/legacy-job/artifact.ext4"
	job, err := store.JobCreate(ctx, account.ID, "legacy-artifact", "batch", ref, []string{"/bin/true"}, 256, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`update jobs set image_materialization_status='verifying_legacy', image_storage_key=image_ref where id=$1::uuid`, job.ID); err != nil {
		t.Fatalf("seed legacy verification: %v", err)
	}
	claimed, err := store.JobClaimLegacyArtifactVerification(ctx, 1, "node-a", time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ImageMaterializationAttempts != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if another, err := store.JobClaimLegacyArtifactVerification(ctx, 1, "node-b", time.Minute); err != nil || len(another) != 0 {
		t.Fatalf("duplicate claim = %+v, %v", another, err)
	}
	promotedKey := "jobs/" + job.ID + ".ext4"
	if _, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, ref, "node-a", ref, true, ""); err == nil {
		t.Fatal("legacy app key accepted as promoted job key")
	}
	if _, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, ref, "node-b", promotedKey, true, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("wrong owner finish = %v, want not found", err)
	}
	deferred, err := store.JobRetryLegacyArtifactVerification(ctx, job.ID, ref, "node-a", "backend unavailable", time.Now().Add(time.Hour))
	if err != nil || deferred.ImageMaterializationStatus != "verifying_legacy" {
		t.Fatalf("retry = %+v, %v", deferred, err)
	}
	if another, err := store.JobClaimLegacyArtifactVerification(ctx, 1, "node-b", time.Minute); err != nil || len(another) != 0 {
		t.Fatalf("backoff claim = %+v, %v", another, err)
	}
	if _, err := pool.Exec(ctx, `update jobs set image_materialization_next_attempt_at=null where id=$1::uuid`, job.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.JobClaimLegacyArtifactVerification(ctx, 1, "node-b", time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ImageMaterializationAttempts != 2 {
		t.Fatalf("reclaim = %+v, %v", claimed, err)
	}
	ready, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, ref, "node-b", promotedKey, true, "")
	if err != nil || ready.ImageMaterializationStatus != "ready" || ready.ImageStorageKey != promotedKey || ready.ImageResolvedDigest != "" {
		t.Fatalf("found artifact = %+v, %v", ready, err)
	}

	if _, err := pool.Exec(ctx,
		`update jobs set image_materialization_status='verifying_legacy', image_storage_key=image_ref, image_materialized_at=null where id=$1::uuid`, job.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.JobClaimLegacyArtifactVerification(ctx, 1, "node-c", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("missing claim = %+v, %v", claimed, err)
	}
	failed, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, ref, "node-c", "", false, "artifact absent")
	if err != nil || failed.ImageMaterializationStatus != "failed" || failed.ImageStorageKey != "" || failed.ImageMaterializationError != "artifact absent" {
		t.Fatalf("missing artifact = %+v, %v", failed, err)
	}
	if rows, err := store.JobClaimLegacyArtifactVerification(context.Background(), 1, "node-d", time.Minute); err != nil || len(rows) != 0 {
		t.Fatalf("terminal claim = %+v, %v", rows, err)
	}
}
