package issuetrackers

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func EnqueueEscalationBatch(ctx context.Context, store state.IssueTrackerEscalationStore) error {
	for i := 0; i < api.IssueTrackerReminderBatch && ctx.Err() == nil; i++ {
		processed, err := store.EnqueueIssueTrackerEscalation(ctx)
		if err != nil {
			return err
		}
		if !processed {
			return nil
		}
	}
	return ctx.Err()
}
