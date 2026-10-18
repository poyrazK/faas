package recovery

import (
	"context"
	"database/sql"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

// AdoptCallbackPolicy requires trusted owner UUIDs, an explicitly selected intent
// metadata snapshot, and a durably saved command UUID. Install the optional adoption
// receipt schema first. Keep these original inputs for retry after unknown COMMIT;
// refreshing the snapshot creates a different command and must use a new UUID.
func AdoptCallbackPolicy(ctx context.Context, db *sql.DB, accountID, appID, commandID string, expected faas.EventAcknowledgmentIntentStatus) (faas.EventAcknowledgmentIntentPolicyAdoptionReceipt, error) {
	return faas.AdoptEventAcknowledgmentIntentRetryPolicy(ctx, db,
		accountID, appID, expected.InvocationID, expected.ReplayGeneration, expected.Attempt,
		faas.EventAcknowledgmentIntentAdoptRetryPolicyOptions{
			CommandID:                commandID,
			ExpectedUpdatedAt:        expected.UpdatedAt,
			ExpectedDeliveryAttempts: expected.DeliveryAttempts,
			Policy: faas.EventAcknowledgmentIntentRetryPolicy{
				InitialBackoff: time.Second,
				MaxBackoff:     5 * time.Minute,
			},
		})
}
