// Package recovery shows one explicitly scheduled callback recovery pass. The
// application supplies its PostgreSQL driver/pool, SDK client and trusted scope.
package recovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"
)

// RecoverCallbacks sends saved callbacks only. Keep db/client available until
// this call returns, then inspect both progress and err before scheduling more.
func RecoverCallbacks(ctx context.Context, db *sql.DB, client *preview.Client, accountID, appID string) (preview.EventAcknowledgmentIntentRecoveryPassResult, error) {
	return preview.RecoverEventAcknowledgmentIntents(ctx, db, client, accountID, appID,
		preview.EventAcknowledgmentIntentRecoveryPassOptions{
			MaxAttempts:   100,
			Concurrency:   4,
			ClaimDuration: 30 * time.Second,
			RetryPolicy: &preview.EventAcknowledgmentIntentRetryPolicy{
				InitialBackoff: time.Second,
				MaxBackoff:     5 * time.Minute,
			},
		})
}
