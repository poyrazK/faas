package appevents

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) applyEventAgeReplay(r *http.Request, store state.EventFanoutReplayStore, account, app string, req api.ReplayEventFanoutFailureRequest) error {
	if req.RecoveryExpectation != nil {
		if !req.RecoveryExpectation.Valid() || req.AllowExpired || req.ExpectedGeneration != nil || req.ExpectedAcceptedAt != nil {
			return state.ErrInvalidArgument
		}
		guarded, ok := store.(state.EventRoutingRecoveryStore)
		if !ok {
			return state.ErrConflict
		}
		return guarded.ReplayFailedWorkflowEventRecipient(r.Context(), account, app, req.EventSource, req.EventID, req.SubscriptionID, *req.RecoveryExpectation)
	}
	if req.ExpectedGeneration != nil {
		if req.ExpectedAcceptedAt == nil || *req.ExpectedGeneration < 0 {
			return state.ErrInvalidArgument
		}
		guarded, ok := store.(state.EventFanoutGenerationGuardedReplayStore)
		if !ok {
			return state.ErrInvalidArgument
		}
		err := guarded.ReplayFailedPublishedEventRecipientWithReplayGuards(r.Context(), account, app, req.EventSource, req.EventID, req.SubscriptionID, req.AllowExpired, *req.ExpectedAcceptedAt, *req.ExpectedGeneration)
		if err == nil && req.AllowExpired {
			s.Host.EmitAudit(r.Context(), "event.subscription.delivery_age.overridden", &account, map[string]any{"app_id": app, "event_source": req.EventSource, "event_id": req.EventID, "subscription_id": req.SubscriptionID})
		}
		return err
	}
	if req.ExpectedAcceptedAt != nil {
		guarded, ok := store.(state.EventFanoutGuardedReplayStore)
		if !ok {
			return state.ErrInvalidArgument
		}
		err := guarded.ReplayFailedPublishedEventRecipientWithAcceptanceGuard(r.Context(), account, app, req.EventSource, req.EventID, req.SubscriptionID, req.AllowExpired, *req.ExpectedAcceptedAt)
		if err == nil && req.AllowExpired {
			s.Host.EmitAudit(r.Context(), "event.subscription.delivery_age.overridden", &account, map[string]any{"app_id": app, "event_source": req.EventSource, "event_id": req.EventID, "subscription_id": req.SubscriptionID})
		}
		return err
	}
	if ageStore, ok := store.(state.EventFanoutAgeReplayStore); ok {
		err := ageStore.ReplayFailedPublishedEventRecipientWithAgeOverride(r.Context(), account, app, req.EventSource, req.EventID, req.SubscriptionID, req.AllowExpired)
		if err == nil && req.AllowExpired {
			s.Host.EmitAudit(r.Context(), "event.subscription.delivery_age.overridden", &account, map[string]any{"app_id": app, "event_source": req.EventSource, "event_id": req.EventID, "subscription_id": req.SubscriptionID})
		}
		return err
	}
	if req.AllowExpired {
		return state.ErrInvalidArgument
	}
	return store.ReplayFailedPublishedEventRecipientForApp(r.Context(), account, app, req.EventSource, req.EventID, req.SubscriptionID)
}
