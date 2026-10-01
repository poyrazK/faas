package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *server) startIssuesMaintenance(ctx context.Context) {
	store, ok := s.store.(interface {
		MaintainIssues(context.Context, time.Time) error
	})
	if !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.IssueMaintenanceInterval)
		defer ticker.Stop()
		for {
			if err := store.MaintainIssues(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
				s.log.Warn("issues maintenance failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
