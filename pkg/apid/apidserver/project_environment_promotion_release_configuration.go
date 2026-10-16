package apidserver

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewPromotionReleaseConfiguration(ctx context.Context, accountID string, plan *projectEnvironmentPromotionPlan, from, to string) (string, *api.Problem) {
	if !plan.SyncConfig {
		return "", nil
	}
	store, ok := s.store.(state.ProjectEnvironmentPromotionReleaseConfigurationStore)
	if !ok {
		return "", nil
	}
	view, err := store.PreviewProjectEnvironmentPromotionReleaseConfiguration(ctx, state.ProjectEnvironmentPromotion{
		ReleaseSecrets: plan.ReleaseSecrets,
		Migrate:        plan.Migrate, Workers: plan.Workers, ResolveServiceBindings: s.promotionServiceGraphEnabled, AccountID: accountID, ProjectID: plan.ProjectID, FromEnvironment: from, ToEnvironment: to, SyncConfig: true, ReleaseGraphMode: plan.ReleaseGraphMode,
		SourceReleaseSetID: projectEnvironmentPromotionReleaseSetID(plan.FromReleaseSet), PreviousTargetReleaseSetID: projectEnvironmentPromotionReleaseSetID(plan.ToReleaseSet)})
	if len(plan.ReleaseSecrets) > 0 {
		if reason := promotionSecretCaptureReason(err); reason != "" {
			return reason, nil
		}
	}
	switch {
	case errors.Is(err, state.ErrReleaseConfigurationTransferUnsupported):
		return "snapshot promotion requires compatible HTTP settings or an explicitly qualified push worker, unchanged destination bindings and credentials, and no sidecars or owned work policies; migrations and workers require their respective opt-ins", nil
	case errors.Is(err, state.ErrInvalidArgument):
		return "source variables conflict with a destination secret or binding, or exceed destination environment limits", nil
	case errors.Is(err, state.ErrConflict):
		return "source release configuration changed or differs from its deployed settings; reconcile and preview again", nil
	case err != nil:
		return "", api.ErrCapacity("could not capture release configuration for promotion")
	}
	if view != nil && view.DestinationConfigHash != "" && !s.promotionServiceGraphEnabled {
		return "service graph promotion is disabled pending native qualification", nil
	}
	plan.ReleaseConfiguration = view
	return "", nil
}

func promotionDestinationConfigHash(p state.ProjectEnvironmentPromotion, plan projectEnvironmentPromotionPlan) string {
	if plan.ReleaseConfiguration != nil && plan.ReleaseConfiguration.DestinationConfigHash != "" {
		return plan.ReleaseConfiguration.DestinationConfigHash
	}
	return p.SourceConfigHash
}

func (s *server) WithPromotionServiceGraphEnabled(enabled bool) *server {
	s.promotionServiceGraphEnabled = enabled
	return s
}

func promotionResolvedConfigHash(plan projectEnvironmentPromotionPlan) string {
	if plan.ReleaseConfiguration == nil {
		return ""
	}
	return plan.ReleaseConfiguration.DestinationConfigHash
}
