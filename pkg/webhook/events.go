package webhook

// Event enqueueing for the app-webhook delivery ledger. Producers call Emit
// after their source-of-truth mutation commits; the dispatcher then owns
// signing, delivery, retries, and replay of the durable row.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

var errInvalidAppWebhookEvent = errors.New("webhook: invalid app webhook event")

// Emit fans an event out to every enabled app webhook whose filter matches.
// An empty filter subscribes to the complete closed vocabulary. The payload is
// stored verbatim in app_webhook_deliveries, making the event replayable from
// the existing customer-facing retry endpoint.
//
// Fan-out is best-effort per subscription: one bad target row does not prevent
// the remaining subscriptions from receiving the event. The returned error
// reports the first failure so producers can log it without rolling back their
// already-committed source mutation.
func Emit(ctx context.Context, store state.Store, appID string, event state.AppWebhookEvent, payload any) error {
	if !state.ValidAppWebhookEvent(event) {
		return fmt.Errorf("%w: %q", errInvalidAppWebhookEvent, event)
	}
	if store == nil {
		return errors.New("webhook: nil store")
	}
	if appID == "" {
		return errors.New("webhook: app id is required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal %s payload: %w", event, err)
	}
	app, err := store.AppByID(ctx, appID)
	if err != nil {
		return fmt.Errorf("webhook: load app %s: %w", appID, err)
	}
	hooks, err := store.ListAppWebhooksForApp(ctx, appID)
	if err != nil {
		return fmt.Errorf("webhook: list app %s subscriptions: %w", appID, err)
	}
	now := time.Now().UTC()
	var firstErr error
	for _, hook := range hooks {
		if !hook.Enabled || !matches(hook.EventFilter, event) {
			continue
		}
		_, err := store.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID: hook.ID,
			AppID:     app.ID,
			AccountID: app.AccountID,
			Event:     event,
			Payload:   json.RawMessage(body),
			Status:    state.AppWebhookDeliveryPending,
			CreatedAt: now,
		})
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("webhook: enqueue %s for %s: %w", event, hook.ID, err)
		}
	}
	return firstErr
}

// EmitTo enqueues one event for a caller-selected app webhook subscription.
// It shares Emit's durable delivery ledger and payload shape, but deliberately
// bypasses event-filter fan-out: an invocation destination is an explicit
// target chosen at enqueue time. Ownership is checked again here because the
// invocation may outlive a webhook update or a scheduler restart.
func EmitTo(ctx context.Context, store state.Store, appID, webhookID string, event state.AppWebhookEvent, payload any) error {
	if !state.ValidAppWebhookEvent(event) {
		return fmt.Errorf("%w: %q", errInvalidAppWebhookEvent, event)
	}
	if store == nil {
		return errors.New("webhook: nil store")
	}
	if appID == "" || webhookID == "" {
		return errors.New("webhook: app and destination are required")
	}
	hook, err := store.AppWebhookByID(ctx, webhookID)
	if err != nil {
		return fmt.Errorf("webhook: load destination %s: %w", webhookID, err)
	}
	if hook.AppID != appID {
		return fmt.Errorf("webhook: destination %s does not belong to app %s", webhookID, appID)
	}
	if !hook.Enabled {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal %s payload: %w", event, err)
	}
	app, err := store.AppByID(ctx, appID)
	if err != nil {
		return fmt.Errorf("webhook: load app %s: %w", appID, err)
	}
	if hook.AccountID != app.AccountID {
		return fmt.Errorf("webhook: destination %s does not belong to app account", webhookID)
	}
	_, err = store.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
		WebhookID: webhookID,
		AppID:     app.ID,
		AccountID: app.AccountID,
		Event:     event,
		Payload:   json.RawMessage(body),
		Status:    state.AppWebhookDeliveryPending,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("webhook: enqueue %s for %s: %w", event, webhookID, err)
	}
	return nil
}

func matches(filter []string, event state.AppWebhookEvent) bool {
	if len(filter) == 0 {
		return true
	}
	for _, candidate := range filter {
		if candidate == string(event) {
			return true
		}
	}
	return false
}
