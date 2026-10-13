// adr: 942
package durable

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func (s *Service) RunBackups(ctx context.Context) {
	if s.Engine == nil || !s.BackupsEnabled {
		return
	}
	cursor := ""
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		scanCtx, cancel := context.WithTimeout(ctx, api.DurableEntityBackupScanTimeout)
		page, err := s.Engine.ScanBackups(scanCtx, cursor, func(id durableentity.ID) bool { return s.Apps[id.AppID] })
		cancel()
		if err == nil {
			cursor = page.NextCursor
		}
		if s.Metrics != nil {
			outcome := "success"
			if err != nil || page.Failed > 0 {
				outcome = "failed"
			}
			s.Metrics.operations.WithLabelValues("backup_scan", outcome).Inc()
		}
		timer.Reset(api.DurableEntityBackupPollInterval)
	}
}
