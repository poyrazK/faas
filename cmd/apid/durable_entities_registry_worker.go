package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
)

func (s *server) runDurableEntityRegistryCleanup(ctx context.Context) {
	if !s.durableEntityRegistryCleanupWorkerEnabled || !s.durableEntityRegistryCleanupEnabled || s.durableEntityTenantQuotas == nil {
		return
	}
	cursor := ""
	failures := 0
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		page, err := s.durableEntityTenantQuotas.SweepRegistryCleanups(ctx, cursor, func(id durableentity.ID) bool { return s.durableEntityApps[id.AppID] })
		if ctx.Err() != nil {
			return
		}
		delay := entitysocket.RegistryCleanupWorkerPoll
		if err != nil {
			cursor = "" // Discovery cursors are disposable; checkpoints are not.
			failures = min(6, failures+1)
			delay = min(5*time.Minute, delay*time.Duration(1<<failures))
			s.log.Warn("durable registry cleanup discovery deferred")
		} else {
			cursor = page.NextCursor
			failures = 0
			if page.Failed > 0 {
				s.log.Warn("durable registry cleanup scopes deferred", "scopes", page.Failed)
			}
		}
		timer.Reset(delay)
	}
}
