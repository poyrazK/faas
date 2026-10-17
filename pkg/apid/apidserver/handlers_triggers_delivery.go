package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// triggerDeliveryOrDefault reads rows created before ADR-1321 as record
// delivery.
func triggerDeliveryOrDefault(delivery string) string {
	if delivery == "" {
		return api.TriggerDeliveryRecord
	}
	return delivery
}

// requestedTriggerDelivery validates a requested delivery mode for the kind;
// nil means record delivery. Batch delivery is a preview until
// FAAS_TRIGGER_BATCH_DELIVERY_PREVIEW_ENABLED=1.
func (s *server) requestedTriggerDelivery(kind api.TriggerKind, requested *string) (string, *api.Problem) {
	if requested == nil {
		return api.TriggerDeliveryRecord, nil
	}
	if err := api.ValidateTriggerDelivery(kind, *requested); err != nil {
		return "", api.NewProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger delivery", err.Error())
	}
	if *requested == api.TriggerDeliveryBatch && !s.triggerBatchDeliveryEnabled {
		return "", api.NewProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger delivery",
			"delivery: batch is a preview that is not enabled on this platform; use delivery: record")
	}
	return *requested, nil
}

func (s *server) setTriggerDelivery(ctx context.Context, id, delivery string) (sqlc.Trigger, error) {
	store, ok := s.store.(state.TriggerDeliveryStore)
	if !ok {
		return sqlc.Trigger{}, errors.New("trigger delivery store unavailable")
	}
	return store.SetTriggerDelivery(ctx, id, delivery)
}

// applyTriggerDelivery switches a just-created, disabled trigger to the
// requested delivery and enables it when asked (ADR-1321). A trigger that
// cannot be switched is deleted again so a half-configured trigger never runs.
func (s *server) applyTriggerDelivery(ctx context.Context, t sqlc.Trigger, delivery string, enable bool) (sqlc.Trigger, *api.Problem) {
	triggerID, appID := uuidFromPgtype(t.ID).String(), uuidFromPgtype(t.AppID).String()
	switched, err := s.setTriggerDelivery(ctx, triggerID, delivery)
	if err == nil && enable {
		switched, err = s.store.UpdateTrigger(ctx, triggerID, &enable, nil, nil, nil, nil, nil, nil, nil, nil)
		if err == nil {
			_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("updated", appID, triggerID))
		}
	}
	if err == nil {
		return switched, nil
	}
	s.log.Warn("trigger delivery switch failed", "trigger", triggerID, "err", err)
	if delErr := s.store.DeleteTrigger(ctx, triggerID, appID); delErr != nil {
		s.log.Warn("trigger cleanup failed", "trigger", triggerID, "err", delErr)
	}
	_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("deleted", appID, triggerID))
	return sqlc.Trigger{}, api.ErrCapacity("could not set trigger delivery")
}
