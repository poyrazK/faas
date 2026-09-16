package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreProjectEnvironmentPromotionRollbackState(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	completedAt := time.Now().UTC().Add(-time.Minute)

	promotion, workloads, err := store.CreateProjectEnvironmentPromotion(ctx, ProjectEnvironmentPromotion{
		AccountID:       "acct-1",
		ProjectID:       "project-1",
		ProjectSlug:     "shop",
		FromEnvironment: "staging",
		ToEnvironment:   "production",
		PromotionHash:   "hash-1",
		IdempotencyKey:  "promotion-1",
		Status:          "succeeded",
		CompletedAt:     &completedAt,
	}, []ProjectEnvironmentPromotionWorkload{{
		WorkloadSlug:               "api",
		WorkloadName:               "api",
		SourceDeploymentID:         "source-1",
		PreviousTargetDeploymentID: "target-old",
		TargetDeploymentID:         "target-new",
		Status:                     "promoted",
	}})
	if err != nil {
		t.Fatalf("CreateProjectEnvironmentPromotion: %v", err)
	}
	if promotion.ID == "" || len(workloads) != 1 || workloads[0].PromotionID != promotion.ID {
		t.Fatalf("created promotion=%+v workloads=%+v", promotion, workloads)
	}
	if _, _, err := store.CreateProjectEnvironmentPromotion(ctx, ProjectEnvironmentPromotion{
		AccountID: "acct-1", IdempotencyKey: "promotion-1",
	}, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate promotion error=%v, want ErrConflict", err)
	}

	got, gotWorkloads, err := store.ProjectEnvironmentPromotionByID(ctx, "acct-1", "shop", "production", promotion.ID)
	if err != nil {
		t.Fatalf("ProjectEnvironmentPromotionByID: %v", err)
	}
	if got.CompletedAt == nil || got.CompletedAt.Equal(completedAt) == false || len(gotWorkloads) != 1 {
		t.Fatalf("promotion lookup=%+v workloads=%+v", got, gotWorkloads)
	}
	if _, _, err := store.ProjectEnvironmentPromotionByID(ctx, "other", "shop", "production", promotion.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-account lookup error=%v, want ErrNotFound", err)
	}
	if _, _, err := store.ProjectEnvironmentPromotionByIdempotencyKey(ctx, "acct-1", "shop", "promotion-1"); err != nil {
		t.Fatalf("ProjectEnvironmentPromotionByIdempotencyKey: %v", err)
	}
	if _, _, err := store.ProjectEnvironmentPromotionByIdempotencyKey(ctx, "acct-1", "shop", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing idempotency lookup error=%v, want ErrNotFound", err)
	}

	if _, err := store.StartProjectEnvironmentPromotionRollback(ctx, "other", promotion.ID, "rollback-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-account rollback start error=%v, want ErrNotFound", err)
	}
	if _, err := store.StartProjectEnvironmentPromotionRollback(ctx, "acct-1", promotion.ID, "rollback-1"); err != nil {
		t.Fatalf("StartProjectEnvironmentPromotionRollback: %v", err)
	}
	if _, err := store.StartProjectEnvironmentPromotionRollback(ctx, "acct-1", promotion.ID, "different-key"); !errors.Is(err, ErrConflict) {
		t.Fatalf("different rollback key error=%v, want ErrConflict", err)
	}

	updatedWorkload, err := store.UpdateProjectEnvironmentPromotionRollbackWorkload(ctx, "acct-1", promotion.ID, workloads[0].ID, "restored", "target-old", "")
	if err != nil {
		t.Fatalf("UpdateProjectEnvironmentPromotionRollbackWorkload: %v", err)
	}
	if updatedWorkload.RollbackStatus != "restored" || updatedWorkload.RestoredTargetDeploymentID != "target-old" {
		t.Fatalf("updated rollback workload=%+v", updatedWorkload)
	}
	if _, err := store.UpdateProjectEnvironmentPromotionRollbackWorkload(ctx, "acct-1", promotion.ID, "missing", "failed", "", "lost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing rollback workload error=%v, want ErrNotFound", err)
	}

	rollbackCompletedAt := time.Now().UTC()
	rolledBack, err := store.UpdateProjectEnvironmentPromotionRollback(ctx, "acct-1", promotion.ID, "rolled_back", "", &rollbackCompletedAt)
	if err != nil {
		t.Fatalf("UpdateProjectEnvironmentPromotionRollback: %v", err)
	}
	if rolledBack.RollbackStatus != "rolled_back" || rolledBack.RollbackCompletedAt == nil {
		t.Fatalf("rolled-back promotion=%+v", rolledBack)
	}
	replayed, err := store.StartProjectEnvironmentPromotionRollback(ctx, "acct-1", promotion.ID, "rollback-1")
	if err != nil || replayed.RollbackStatus != "rolled_back" {
		t.Fatalf("rollback replay=%+v err=%v", replayed, err)
	}

	updated, err := store.UpdateProjectEnvironmentPromotion(ctx, "acct-1", promotion.ID, "failed", "promotion failed", nil)
	if err != nil || updated.Status != "failed" || updated.Error != "promotion failed" {
		t.Fatalf("UpdateProjectEnvironmentPromotion=%+v err=%v", updated, err)
	}
	if _, err := store.UpdateProjectEnvironmentPromotion(ctx, "other", promotion.ID, "failed", "", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-account promotion update error=%v, want ErrNotFound", err)
	}
	updatedWorkload, err = store.UpdateProjectEnvironmentPromotionWorkload(ctx, "acct-1", promotion.ID, workloads[0].ID, "failed", "", "promotion failed")
	if err != nil || updatedWorkload.Status != "failed" || updatedWorkload.Error != "promotion failed" {
		t.Fatalf("UpdateProjectEnvironmentPromotionWorkload=%+v err=%v", updatedWorkload, err)
	}
	if _, err := store.UpdateProjectEnvironmentPromotionWorkload(ctx, "acct-1", promotion.ID, "missing", "failed", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing promotion workload error=%v, want ErrNotFound", err)
	}
}
