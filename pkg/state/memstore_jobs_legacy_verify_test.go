package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreLegacyJobArtifactVerification(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "legacy-artifact@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(ctx, account.ID, "legacy-artifact", "batch", "apps/legacy-job/artifact.ext4", []string{"/bin/true"}, 256, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	(*store.Mu()).Lock()
	job.ImageMaterializationStatus = "verifying_legacy"
	job.ImageStorageKey = job.ImageRef
	(*store.Jobs())[job.ID] = job
	(*store.Mu()).Unlock()

	claimed, err := store.JobClaimLegacyArtifactVerification(ctx, 1, "node-a", time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ImageMaterializationAttempts != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if another, err := store.JobClaimLegacyArtifactVerification(ctx, 1, "node-b", time.Minute); err != nil || len(another) != 0 {
		t.Fatalf("duplicate claim = %+v, %v", another, err)
	}
	promotedKey := "jobs/" + job.ID + ".ext4"
	if _, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, job.ImageRef, "node-a", job.ImageRef, true, ""); err == nil {
		t.Fatal("legacy app key accepted as promoted job key")
	}
	if _, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, job.ImageRef, "node-b", promotedKey, true, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("other owner finish error = %v, want conflict", err)
	}
	if _, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, "apps/other/artifact.ext4", "node-a", promotedKey, true, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("other ref finish error = %v, want conflict", err)
	}
	deferred, err := store.JobRetryLegacyArtifactVerification(ctx, job.ID, job.ImageRef, "node-a", "backend unavailable", time.Now().Add(time.Hour))
	if err != nil || deferred.ImageMaterializationStatus != "verifying_legacy" {
		t.Fatalf("retry = %+v, %v", deferred, err)
	}
	if another, err := store.JobClaimLegacyArtifactVerification(ctx, 1, "node-b", time.Minute); err != nil || len(another) != 0 {
		t.Fatalf("backoff claim = %+v, %v", another, err)
	}
	(*store.Mu()).Lock()
	deferred.ImageMaterializationNextAttemptAt = nil
	(*store.Jobs())[job.ID] = deferred
	(*store.Mu()).Unlock()
	claimed, err = store.JobClaimLegacyArtifactVerification(ctx, 1, "node-b", time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ImageMaterializationAttempts != 2 {
		t.Fatalf("reclaim = %+v, %v", claimed, err)
	}
	ready, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, job.ImageRef, "node-b", promotedKey, true, "")
	if err != nil || ready.ImageMaterializationStatus != "ready" || ready.ImageStorageKey != promotedKey || ready.ImageResolvedDigest != "" {
		t.Fatalf("found artifact = %+v, %v", ready, err)
	}
	(*store.Mu()).Lock()
	ready.ImageMaterializationStatus = "verifying_legacy"
	ready.ImageStorageKey = job.ImageRef
	(*store.Jobs())[job.ID] = ready
	(*store.Mu()).Unlock()
	claimed, err = store.JobClaimLegacyArtifactVerification(ctx, 1, "node-c", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("missing claim = %+v, %v", claimed, err)
	}
	failed, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, job.ImageRef, "node-c", "", false, "artifact absent")
	if err != nil || failed.ImageMaterializationStatus != "failed" || failed.ImageStorageKey != "" || failed.ImageMaterializationError != "artifact absent" {
		t.Fatalf("missing artifact = %+v, %v", failed, err)
	}
}
