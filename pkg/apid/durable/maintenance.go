// adr: 712
package durable

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func (s *Service) RunMaintenance(ctx context.Context) {
	if s.Engine == nil || !s.MaintenanceEnabled {
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
		started := time.Now()
		result, err := s.Engine.MaintenanceStep(ctx, s.Owner, s.Apps)
		s.Metrics.ObserveMaintenance(result, err)
		s.Metrics.observeDuration("maintenance", started)
		if ctx.Err() == nil && (result.Failed > 0 || result.Cleanup.Failed > 0 || err != nil && !errors.Is(err, durableentity.ErrBusy) && !errors.Is(err, durableentity.ErrConflict)) {
			// Provider errors and storage paths may expose credentials or identities.
			s.Host.Logger().Warn("durable entity maintenance deferred")
		}
		timer.Reset(api.DurableEntityMaintenancePollInterval)
	}
}
