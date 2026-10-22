package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// Recovery uses scheduler-owned VM routing and never dispatches a command.
func (c *AppTaskCoordinator) quiescePromotionMigrations(ctx context.Context) error {
	store, ok := c.store.(state.PromotionMigrationRuntimeStore)
	if !ok {
		return nil
	}
	backend, ok := c.backend.(interface {
		QuiescePromotionMigration(context.Context, state.PromotionMigrationQuiescenceJob) error
	})
	if !ok {
		return nil
	}
	jobs, err := store.PendingPromotionMigrationQuiescence(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, job := range jobs {
		call, cancel := context.WithTimeout(ctx, c.config.DestroyTimeout)
		err = backend.QuiescePromotionMigration(call, job)
		cancel()
		if err == nil {
			err = store.AcknowledgePromotionMigrationQuiescence(ctx, job)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
