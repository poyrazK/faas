package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetriage"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

func (s *server) startIssueApplicationReminders(ctx context.Context) {
	st, ok := s.store.(state.IssueApplicationReminderStore)
	if !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.IssueMaintenanceInterval)
		defer ticker.Stop()
		for {
			batch, cancel := context.WithTimeout(ctx, api.IssueMaintenanceInterval)
			if err := issuetriage.EnqueueReminderBatch(batch, st); err != nil && ctx.Err() == nil {
				s.log.Warn("native issue reminder enqueue failed")
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
