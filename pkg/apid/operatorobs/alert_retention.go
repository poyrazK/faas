package operatorobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// RunAlertRetention runs even when Prometheus is disabled. Separate deadlines
// and a separate goroutine keep failed collection from stopping cleanup.
func RunAlertRetention(ctx context.Context, store state.OperatorAlertRetentionStore, log *slog.Logger, metrics *MaintenanceMetrics) {
	ticker := time.NewTicker(api.ObsAlertRetentionInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		err := RetainAlertsOnce(ctx, store, started)
		metrics.record("retention", started, err)
		if err != nil && ctx.Err() == nil {
			log.Warn("operator alert history retention failed", "reason", "cleanup_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RetainAlertsOnce acknowledges at most one cleanup batch. Successful deletion
// and its health receipt commit together; failed attempts use a fresh bounded
// context so an expired deletion budget cannot suppress the failure receipt.
func RetainAlertsOnce(ctx context.Context, store state.OperatorAlertRetentionStore, now time.Time) error {
	pruneCtx, cancel := context.WithTimeout(ctx, api.ObsAlertRetentionBudget)
	_, err := store.PruneOperatorAlertHistory(pruneCtx, now.Add(-api.ObsAlertHistoryRetention), api.ObsAlertHistoryPruneBatch)
	cancel()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failureCtx, cancelFailure := context.WithTimeout(ctx, api.ObsAlertMaintenanceReadBudget)
	defer cancelFailure()
	if recordErr := store.RecordOperatorAlertRetentionFailure(failureCtx, now); recordErr != nil {
		return fmt.Errorf("record failed alert retention: %w", recordErr)
	}
	return fmt.Errorf("prune operator alert history: %w", err)
}
