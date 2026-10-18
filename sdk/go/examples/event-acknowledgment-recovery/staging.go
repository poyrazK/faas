package recovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

// StageCompletionWithPolicy requires a worker reconstructed from an authenticated
// protected job. Install/upgrade the intent schema and use binding-aware recovery
// hosts before enabling this staging path. Handler writes only through tx.
func StageCompletionWithPolicy(ctx context.Context, db *sql.DB, worker *faas.EventAcknowledgmentWorker, handler func(context.Context, faas.EventConsumerSQLTransaction) (json.RawMessage, error)) (faas.EventAcknowledgmentIntentResult, error) {
	return worker.RunDurableWithRetryPolicy(ctx, db,
		faas.EventAcknowledgmentIntentRetryPolicy{
			InitialBackoff: time.Second,
			MaxBackoff:     5 * time.Minute,
		}, handler)
}
