package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

type EnvironmentGitOpsPromotionStore interface {
	PreviewEnvironmentGitOpsPromotion(context.Context, EnvironmentGitSource, EnvironmentGitSource, api.PreviewEnvironmentGitPromotionRequest, environmentsync.DesiredState) (api.PreviewEnvironmentGitPromotionResponse, error)
}

func validateGitPromotion(from, to EnvironmentGitSource, request api.PreviewEnvironmentGitPromotionRequest, desired environmentsync.DesiredState) error {
	if !api.ValidProjectEnvironmentSlug(request.FromEnvironment) || request.FromEnvironment != from.EnvironmentSlug || !environmentCommitRE.MatchString(request.TargetCommitSHA) ||
		from.ID == to.ID || from.EnvironmentID == to.EnvironmentID || from.AccountID != to.AccountID || from.ProjectID != to.ProjectID ||
		request.ExpectedPlanHash != "" && !environmentDefinitionDigestRE.MatchString(request.ExpectedPlanHash) {
		return ErrInvalidArgument
	}
	return validateEnvironmentGitOpsPreview(to, request.TargetCommitSHA, desired)
}

func promotionSourceMatches(current, expected EnvironmentGitSource) bool {
	return current.ID == expected.ID && current.AccountID == expected.AccountID && current.ProjectID == expected.ProjectID && current.EnvironmentID == expected.EnvironmentID &&
		current.EnvironmentSlug == expected.EnvironmentSlug && current.Generation == expected.Generation && current.IntentVersion == expected.IntentVersion &&
		current.ApprovedRevisionID == expected.ApprovedRevisionID && current.Spec == expected.Spec && !current.Detached
}

func finishGitPromotion(input promotion.Input, out api.PreviewEnvironmentGitPromotionResponse, desired environmentsync.DesiredState, observed EnvironmentGitOpsObservation, expected string) (api.PreviewEnvironmentGitPromotionResponse, error) {
	preview, err := environmentGitOpsCommitPreview(input.To, input.TargetCommit, desired, observed)
	if err != nil {
		return out, err
	}
	out, err = promotion.Finish(out, preview.Plan)
	if err == nil && expected != "" && expected != out.PlanHash {
		return api.PreviewEnvironmentGitPromotionResponse{}, ErrConflict
	}
	return out, err
}

func promotionInput(from, to EnvironmentGitSource, revision EnvironmentDesiredRevision, request api.PreviewEnvironmentGitPromotionRequest, desired environmentsync.DesiredState) promotion.Input {
	return promotion.Input{From: from, To: to, Revision: revision, Target: desired, TargetCommit: request.TargetCommitSHA, Now: time.Now().UTC()}
}
