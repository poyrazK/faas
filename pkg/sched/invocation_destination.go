package sched

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhook"
)

// enqueueInvocationDestination sends one terminal invocation outcome to the
// webhook selected when the invocation was created. The source transition is
// already durable when this helper runs; webhook delivery has its own retry
// and dead-letter ledger, so enqueue failures are returned for logging rather
// than fed back into the invocation state machine.
func enqueueInvocationDestination(
	ctx context.Context,
	store state.Store,
	now func() time.Time,
	inv state.Invocation,
	outcome state.InvocationOutcome,
	result json.RawMessage,
	lastError string,
) error {
	if store == nil {
		return nil
	}
	destination := inv.OnSuccessDestinationID
	status := "succeeded"
	if outcome != state.OutcomeSuccess {
		destination = inv.OnFailureDestinationID
		status = string(outcome)
		if status == "" {
			status = "failed"
		}
	}
	if destination == "" {
		return nil
	}
	finishedAt := time.Now().UTC()
	if now != nil {
		finishedAt = now().UTC()
	}
	payload := map[string]any{
		"job_id":      inv.ID,
		"run_id":      inv.ID,
		"app_id":      inv.AppID,
		"account_id":  inv.AccountID,
		"source":      string(inv.Source),
		"status":      status,
		"outcome":     string(outcome),
		"finished_at": finishedAt,
		"attempts":    inv.Attempts,
	}
	if len(result) > 0 {
		payload["result"] = json.RawMessage(result)
	}
	if lastError != "" {
		payload["error"] = lastError
	}
	return webhook.EmitTo(ctx, store, inv.AppID, destination, state.AppWebhookEventJobFinished, payload)
}
