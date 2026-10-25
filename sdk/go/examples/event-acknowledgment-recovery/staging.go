package recovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"
)

// StageCompletionWithPolicy requires a worker reconstructed from an authenticated
// protected job. Install/upgrade the intent schema and use binding-aware recovery
// hosts before enabling this staging path. Handler writes only through tx.
func StageCompletionWithPolicy(ctx context.Context, db *sql.DB, worker *preview.EventAcknowledgmentWorker, handler func(context.Context, preview.EventConsumerSQLTransaction) (json.RawMessage, error)) (preview.EventAcknowledgmentIntentResult, error) {
	return worker.RunDurableWithRetryPolicy(ctx, db,
		preview.EventAcknowledgmentIntentRetryPolicy{
			InitialBackoff: time.Second,
			MaxBackoff:     5 * time.Minute,
		}, handler)
}
