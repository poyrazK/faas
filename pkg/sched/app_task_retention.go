package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// appTaskPruneInterval is how often schedd deletes finished tasks past
	// their plan's retention. Retention is measured in days, so a pass every
	// ten minutes keeps rows at most minutes past it.
	appTaskPruneInterval = 10 * time.Minute
	// appTaskPruneBatch and appTaskPruneMaxBatches bound one pass per plan,
	// so a large first backlog drains over several passes instead of one
	// long delete.
	appTaskPruneBatch      = 500
	appTaskPruneMaxBatches = 20
)

// PruneOnce deletes finished manual and cron tasks past each plan's
// Limits.AppTaskRetentionDays and returns how many it deleted. A store
// without AppTaskRetentionStore prunes nothing.
func (c *AppTaskCoordinator) PruneOnce(ctx context.Context) (int, error) {
	if c == nil || c.store == nil {
		return 0, ErrAppTaskCoordinatorNotWired
	}
	pruner, ok := c.store.(state.AppTaskRetentionStore)
	if !ok {
		return 0, nil
	}
	now := c.now().UTC()
	total := 0
	for _, plan := range api.Plans {
		limits, ok := api.LimitsFor(plan)
		if !ok || limits.AppTaskRetentionDays <= 0 {
			continue
		}
		params := state.PruneAppTasksParams{
			Plan:           plan,
			FinishedBefore: now.AddDate(0, 0, -limits.AppTaskRetentionDays),
			Limit:          appTaskPruneBatch,
		}
		for range appTaskPruneMaxBatches {
			deleted, err := pruner.PruneAppTasks(ctx, params)
			total += deleted
			if err != nil {
				return total, fmt.Errorf("sched: prune %s app tasks: %w", plan, err)
			}
			if deleted < appTaskPruneBatch {
				break
			}
		}
	}
	return total, nil
}

func (c *AppTaskCoordinator) runPruner(ctx context.Context) {
	ticker := time.NewTicker(appTaskPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err := c.PruneOnce(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				c.log.Warn("schedd: app task retention sweep failed", "error_class", appTaskErrorClass(err), "deleted", deleted)
				continue
			}
			if deleted > 0 {
				c.log.Info("schedd: pruned app tasks past retention", "deleted", deleted)
			}
		}
	}
}
