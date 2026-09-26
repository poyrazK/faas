package state

import (
	"encoding/json"
	"time"
)

// invocationDestinationDelivery builds the durable job.finished delivery for
// a terminal invocation. Callers persist this row in the same atomic boundary
// as the invocation's terminal state transition.
func invocationDestinationDelivery(inv Invocation) (AppWebhookDelivery, bool, error) {
	outcome := OutcomeFailed
	if inv.Outcome != nil {
		outcome = *inv.Outcome
	} else if inv.State == InvocationCompleted {
		outcome = OutcomeSuccess
	}

	destination := inv.OnFailureDestinationID
	status := string(outcome)
	if outcome == OutcomeSuccess {
		destination = inv.OnSuccessDestinationID
		status = "succeeded"
	}
	if destination == "" {
		return AppWebhookDelivery{}, false, nil
	}

	finishedAt := time.Now().UTC()
	if inv.CompletedAt != nil {
		finishedAt = inv.CompletedAt.UTC()
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
	if outcome == OutcomeSuccess && len(inv.Result) > 0 {
		payload["result"] = json.RawMessage(inv.Result)
	}
	if outcome != OutcomeSuccess && inv.LastError != "" {
		payload["error"] = inv.LastError
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return AppWebhookDelivery{}, false, err
	}
	return AppWebhookDelivery{
		WebhookID:     destination,
		AppID:         inv.AppID,
		AccountID:     inv.AccountID,
		Event:         AppWebhookEventJobFinished,
		Payload:       body,
		Status:        AppWebhookDeliveryPending,
		NextAttemptAt: finishedAt,
		CreatedAt:     finishedAt,
	}, true, nil
}
