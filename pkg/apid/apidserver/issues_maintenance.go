package apidserver

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
	maintain := store.MaintainIssues
	if retention, ok := s.store.(interface {
		MaintainIssueRetention(context.Context, time.Time) (bool, error)
		MaintainIssueHousekeeping(context.Context, time.Time) error
	}); ok {
		maintain = retention.MaintainIssueHousekeeping
		go func() {
			delay := time.Duration(0)
			for {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				batch, cancel := context.WithTimeout(ctx, 10*time.Second)
				more, err := retention.MaintainIssueRetention(batch, time.Now().UTC())
				cancel()
				delay = api.IssueMaintenanceInterval
				if err != nil {
					if ctx.Err() == nil {
						s.log.Warn("issues retention cleanup failed", "err", err)
					}
				} else if more {
					delay = time.Second
				}
			}
		}()
	}
	go func() {
		ticker := time.NewTicker(api.IssueMaintenanceInterval)
		defer ticker.Stop()
		for {
			if err := maintain(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
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
