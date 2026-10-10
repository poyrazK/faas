// adr: 942
package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func (s *server) runDurableEntityBackups(ctx context.Context) {
	if s.durableEntities == nil || !s.durableEntityBackupsEnabled {
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
		page, err := s.durableEntities.ScanBackups(scanCtx, cursor, func(id durableentity.ID) bool { return s.durableEntityApps[id.AppID] })
		cancel()
		if err == nil {
			cursor = page.NextCursor
		}
		if s.durableEntityMetrics != nil {
			outcome := "success"
			if err != nil || page.Failed > 0 {
				outcome = "failed"
			}
			s.durableEntityMetrics.operations.WithLabelValues("backup_scan", outcome).Inc()
		}
		timer.Reset(api.DurableEntityBackupPollInterval)
	}
}
