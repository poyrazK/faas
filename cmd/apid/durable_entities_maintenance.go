// adr: 638
package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func (s *server) runDurableEntityMaintenance(ctx context.Context) {
	if s.durableEntities == nil || !s.durableEntityMaintenanceEnabled {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		result, err := s.durableEntities.MaintenanceStep(ctx, s.durableEntityOwner, s.durableEntityApps)
		s.durableEntityMetrics.observeMaintenance(result, err)
		if ctx.Err() == nil && (result.Failed > 0 || result.Cleanup.Failed > 0 || err != nil && !errors.Is(err, durableentity.ErrBusy) && !errors.Is(err, durableentity.ErrConflict)) {
			// Provider errors and storage paths may expose credentials or identities.
			s.log.Warn("durable entity maintenance deferred")
		}
		timer.Reset(api.DurableEntityMaintenancePollInterval)
	}
}
