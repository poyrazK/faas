package recovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"
)

// BudgetAdoptionCommand builds an explicit decision from selected metadata. The
// caller authorizes it and durably saves the key, owner scope and returned options
// before execution. A v1 intent keeps its timing; unbound work uses default timing.
func BudgetAdoptionCommand(commandID string, selected preview.EventAcknowledgmentIntentStatus, total int64) preview.EventAcknowledgmentIntentAdoptRetryBudgetOptions {
	options := preview.EventAcknowledgmentIntentAdoptRetryBudgetOptions{
		CommandID: commandID, ExpectedUpdatedAt: selected.UpdatedAt,
		ExpectedDeliveryAttempts: selected.DeliveryAttempts,
		Budget:                   preview.EventAcknowledgmentIntentRetryBudget{MaxDeliveryAttempts: total},
	}
	if selected.RetryPolicy != nil {
		policy := *selected.RetryPolicy
		options.ExpectedPolicy = &policy
		options.Budget.Policy = preview.EventAcknowledgmentIntentRetryPolicy{
			InitialBackoff: time.Duration(policy.InitialBackoffMS) * time.Millisecond,
			MaxBackoff:     time.Duration(policy.MaxBackoffMS) * time.Millisecond,
		}
	}
	return options
}

// ApplyBudgetAdoption requires a durably saved, authorized command and trusted
// owner scope. Retry these original inputs after uncertainty; never refresh a
// fence under the same command ID. Install the optional budget-adoption schema.
func ApplyBudgetAdoption(ctx context.Context, db *sql.DB, accountID, appID, invocationID string, generation int64, attempt int, saved preview.EventAcknowledgmentIntentAdoptRetryBudgetOptions) (preview.EventAcknowledgmentIntentBudgetAdoptionReceipt, error) {
	return preview.AdoptEventAcknowledgmentIntentRetryBudget(ctx, db,
		accountID, appID, invocationID, generation, attempt, saved)
}
