package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreLegacyJobArtifactVerification(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "legacy-artifact@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(ctx, account.ID, "legacy-artifact", "batch", "apps/legacy-job/artifact.ext4", []string{"/bin/true"}, 256, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	job.ImageMaterializationStatus = "verifying_legacy"
	job.ImageStorageKey = job.ImageRef
	store.jobs[job.ID] = job
	store.mu.Unlock()

	claimed, err := store.JobClaimLegacyArtifactVerification(ctx, 1, "node-a", time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ImageMaterializationAttempts != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if another, err := store.JobClaimLegacyArtifactVerification(ctx, 1, "node-b", time.Minute); err != nil || len(another) != 0 {
		t.Fatalf("duplicate claim = %+v, %v", another, err)
	}
	if _, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, job.ImageRef, "node-b", true, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("other owner finish error = %v, want conflict", err)
	}
	if _, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, "apps/other/artifact.ext4", "node-a", true, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("other ref finish error = %v, want conflict", err)
	}
	deferred, err := store.JobRetryLegacyArtifactVerification(ctx, job.ID, job.ImageRef, "node-a", "backend unavailable", time.Now().Add(time.Hour))
	if err != nil || deferred.ImageMaterializationStatus != "verifying_legacy" {
		t.Fatalf("retry = %+v, %v", deferred, err)
	}
	if another, err := store.JobClaimLegacyArtifactVerification(ctx, 1, "node-b", time.Minute); err != nil || len(another) != 0 {
		t.Fatalf("backoff claim = %+v, %v", another, err)
	}
	store.mu.Lock()
	deferred.ImageMaterializationNextAttemptAt = nil
	store.jobs[job.ID] = deferred
	store.mu.Unlock()
	claimed, err = store.JobClaimLegacyArtifactVerification(ctx, 1, "node-b", time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ImageMaterializationAttempts != 2 {
		t.Fatalf("reclaim = %+v, %v", claimed, err)
	}
	ready, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, job.ImageRef, "node-b", true, "")
	if err != nil || ready.ImageMaterializationStatus != "ready" || ready.ImageStorageKey != job.ImageRef || ready.ImageResolvedDigest != "" {
		t.Fatalf("found artifact = %+v, %v", ready, err)
	}
	store.mu.Lock()
	ready.ImageMaterializationStatus = "verifying_legacy"
	store.jobs[job.ID] = ready
	store.mu.Unlock()
	claimed, err = store.JobClaimLegacyArtifactVerification(ctx, 1, "node-c", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("missing claim = %+v, %v", claimed, err)
	}
	failed, err := store.JobFinishLegacyArtifactVerification(ctx, job.ID, job.ImageRef, "node-c", false, "artifact absent")
	if err != nil || failed.ImageMaterializationStatus != "failed" || failed.ImageStorageKey != "" || failed.ImageMaterializationError != "artifact absent" {
		t.Fatalf("missing artifact = %+v, %v", failed, err)
	}
}
