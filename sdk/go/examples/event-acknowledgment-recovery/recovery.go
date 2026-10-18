// Package recovery shows one explicitly scheduled callback recovery pass. The
// application supplies its PostgreSQL driver/pool, SDK client and trusted scope.
package recovery

import (
	"context"
	"database/sql"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

// RecoverCallbacks sends saved callbacks only. Keep db/client available until
// this call returns, then inspect both progress and err before scheduling more.
func RecoverCallbacks(ctx context.Context, db *sql.DB, client *faas.Client, accountID, appID string) (faas.EventAcknowledgmentIntentRecoveryPassResult, error) {
	return faas.RecoverEventAcknowledgmentIntents(ctx, db, client, accountID, appID,
		faas.EventAcknowledgmentIntentRecoveryPassOptions{
			MaxAttempts:   100,
			Concurrency:   4,
			ClaimDuration: 30 * time.Second,
			RetryPolicy: &faas.EventAcknowledgmentIntentRetryPolicy{
				InitialBackoff: time.Second,
				MaxBackoff:     5 * time.Minute,
			},
		})
}
