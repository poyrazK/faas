package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) startIssueTrackerReminders(ctx context.Context) {
	store, ok := s.store.(state.IssueTrackerReminderStore)
	if !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.IssueMaintenanceInterval)
		defer ticker.Stop()
		for {
			batch, cancel := context.WithTimeout(ctx, api.IssueMaintenanceInterval)
			if err := issuetrackers.EnqueueReminderBatch(batch, store); err != nil && ctx.Err() == nil {
				s.log.Warn("tracker reminder enqueue failed")
			}
			if escalation, ok := s.store.(state.IssueTrackerEscalationStore); ok && batch.Err() == nil {
				if err := issuetrackers.EnqueueEscalationBatch(batch, escalation); err != nil && ctx.Err() == nil {
					s.log.Warn("tracker escalation enqueue failed")
				}
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
