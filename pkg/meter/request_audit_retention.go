package meter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// RequestAuditRetention is an initial, bounded privacy policy for the
// opt-in exact-request collection. Financial usage facts keep their own
// retention; deleting an audit row never deletes its usage event.
const RequestAuditRetention = 30 * 24 * time.Hour

const requestAuditRetentionBatchSQL = `DELETE FROM public.request_audit_events
WHERE ctid IN (
    SELECT ctid FROM public.request_audit_events
    WHERE occurred_at < now() - interval '30 days'
    LIMIT $1
)`

func RetentionOnceRequestAudit(ctx context.Context, db retentionExecer) (int64, error) {
	var total int64
	for i := 0; i < MaxRetentionBatches; i++ {
		count, err := db.Exec(ctx, requestAuditRetentionBatchSQL, RetentionBatchSize)
		if err != nil {
			return total, fmt.Errorf("request audit retention batch %d: %w", i, err)
		}
		total += count
		if count < RetentionBatchSize {
			return total, nil
		}
	}
	return total, ErrRetentionBatchCap
}

func RetentionLoopRequestAudit(ctx context.Context, db retentionExecer, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, err := RetentionOnceRequestAudit(ctx, db)
			if log == nil {
				continue
			}
			switch {
			case err == nil:
				log.Info("request audit retention tick ok", "rows_deleted", rows)
			case errors.Is(err, ErrRetentionBatchCap):
				log.Warn("request audit retention batch cap", "rows_deleted", rows, "err", err)
			default:
				log.Error("request audit retention tick failed", "err", err)
			}
		}
	}
}
