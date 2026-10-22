package recovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

// StageCompletionWithRetryBudget requires the upgraded schema and exclusively
// budget-aware workers for this scope. Handler writes only through tx.
func StageCompletionWithRetryBudget(ctx context.Context, db *sql.DB, worker *faas.EventAcknowledgmentWorker, handler func(context.Context, faas.EventConsumerSQLTransaction) (json.RawMessage, error)) (faas.EventAcknowledgmentIntentResult, error) {
	return worker.RunDurableWithRetryBudget(ctx, db, faas.EventAcknowledgmentIntentRetryBudget{
		Policy: faas.EventAcknowledgmentIntentRetryPolicy{
			InitialBackoff: time.Second,
			MaxBackoff:     5 * time.Minute,
		},
		MaxDeliveryAttempts: 8,
	}, handler)
}

// ResumeCallbackBudget requires an explicitly selected parked metadata snapshot,
// trusted owner UUIDs and a durably saved command UUID/new total limit. Keep the
// original inputs when resolving unknown COMMIT; a new decision uses a new UUID.
func ResumeCallbackBudget(ctx context.Context, db *sql.DB, accountID, appID, commandID string, selected faas.EventAcknowledgmentIntentStatus, newLimit int64) (faas.EventAcknowledgmentIntentRetryResumeReceipt, error) {
	return faas.ResumeEventAcknowledgmentIntentRetryBudget(ctx, db,
		accountID, appID, selected.InvocationID, selected.ReplayGeneration, selected.Attempt,
		faas.EventAcknowledgmentIntentResumeRetryBudgetOptions{
			CommandID:                    commandID,
			ExpectedUpdatedAt:            selected.UpdatedAt,
			ExpectedDeliveryAttempts:     selected.DeliveryAttempts,
			ExpectedDeliveryAttemptLimit: selected.DeliveryAttemptLimit,
			NewDeliveryAttemptLimit:      newLimit,
		})
}
