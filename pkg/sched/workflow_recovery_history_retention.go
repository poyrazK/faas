package sched

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

func (l *Loop) pruneWorkflowRecoveryHistory(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.WorkflowRecoveryHistoryRetentionStore)
	if !ok {
		return
	}
	if _, err := store.PruneWorkflowRecoveryHistory(ctx, now); err != nil {
		if l.log != nil {
			l.log.Warn("sched: prune recovery automation history", "err", err)
		}
	}
}
