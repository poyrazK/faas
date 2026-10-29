package recovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"
)

// AdoptCallbackPolicy requires trusted owner UUIDs, an explicitly selected intent
// metadata snapshot, and a durably saved command UUID. Install the optional adoption
// receipt schema first. Keep these original inputs for retry after unknown COMMIT;
// refreshing the snapshot creates a different command and must use a new UUID.
func AdoptCallbackPolicy(ctx context.Context, db *sql.DB, accountID, appID, commandID string, expected preview.EventAcknowledgmentIntentStatus) (preview.EventAcknowledgmentIntentPolicyAdoptionReceipt, error) {
	return preview.AdoptEventAcknowledgmentIntentRetryPolicy(ctx, db,
		accountID, appID, expected.InvocationID, expected.ReplayGeneration, expected.Attempt,
		preview.EventAcknowledgmentIntentAdoptRetryPolicyOptions{
			CommandID:                commandID,
			ExpectedUpdatedAt:        expected.UpdatedAt,
			ExpectedDeliveryAttempts: expected.DeliveryAttempts,
			Policy: preview.EventAcknowledgmentIntentRetryPolicy{
				InitialBackoff: time.Second,
				MaxBackoff:     5 * time.Minute,
			},
		})
}
