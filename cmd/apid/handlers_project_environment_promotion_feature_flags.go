package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) promotionFeatureFlagIdentity(ctx context.Context, environment state.ProjectEnvironment) (string, *api.Problem) {
	store, ok := s.store.(state.FeatureFlagStore)
	if !ok {
		return "", api.ErrCapacity("environment feature flag inventory is unavailable")
	}
	version, err := store.GetFeatureFlags(ctx, state.FeatureFlagScope{
		AccountID: environment.AccountID, ProjectID: environment.ProjectID, EnvironmentID: environment.ID,
	}, 0)
	if err != nil {
		return "", api.ErrCapacity("could not inspect environment feature flags")
	}
	hash, err := state.FeatureFlagsPromotionHash(version)
	if err != nil {
		return "", api.ErrCapacity("environment feature flag identity is invalid")
	}
	return hash, nil
}

func (s *server) loadPromotionFeatureFlagIdentities(ctx context.Context, plan *projectEnvironmentPromotionPlan, source, target state.ProjectEnvironment) *api.Problem {
	if !plan.SyncConfig {
		return nil
	}
	var problem *api.Problem
	plan.SourceFeatureFlagsHash, problem = s.promotionFeatureFlagIdentity(ctx, source)
	if problem != nil {
		return problem
	}
	plan.TargetFeatureFlagsHash, problem = s.promotionFeatureFlagIdentity(ctx, target)
	return problem
}

func (s *server) resumePromotionFeatureFlagIdentities(ctx context.Context, plan *projectEnvironmentPromotionPlan, promotion state.ProjectEnvironmentPromotion, source, target state.ProjectEnvironment) *api.Problem {
	if !promotion.SyncConfig {
		return nil
	}
	store, ok := s.store.(state.ProjectEnvironmentPromotionFeatureFlagStore)
	if !ok {
		return api.ErrCapacity("promotion feature flag checkpoint inventory is unavailable")
	}
	receipt, err := store.ProjectEnvironmentPromotionFeatureFlags(ctx, promotion.AccountID, promotion.ID)
	if err != nil {
		return promotionResumeConflict("the promotion has no authenticated feature flag snapshot; start a new promotion")
	}
	if promotion.TargetReleaseSetID == "" {
		if problem := s.loadPromotionFeatureFlagIdentities(ctx, plan, source, target); problem != nil {
			return problem
		}
		if plan.SourceFeatureFlagsHash != receipt.SourceHash || plan.TargetFeatureFlagsHash != receipt.PreviousTargetHash || receipt.TargetVersion != 0 {
			return promotionResumeConflict("feature flags changed after the promotion started; preview and promote again")
		}
	} else if receipt.TargetVersion == 0 || receipt.RollbackVersion != 0 {
		return promotionResumeConflict("the target graph has no matching feature flag activation receipt")
	}
	plan.SourceFeatureFlagsHash, plan.TargetFeatureFlagsHash = receipt.SourceHash, receipt.PreviousTargetHash
	return nil
}
