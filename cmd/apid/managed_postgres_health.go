package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func managedPostgresHealthView(health managedpostgres.HealthSummary) *api.ManagedPostgresHealth {
	out := &api.ManagedPostgresHealth{Enabled: health.Enabled, Status: health.Status, Fresh: health.Fresh,
		ProviderStatus: health.ProviderStatus, ComputeState: string(health.ComputeState),
		StaleAfterSeconds: health.StaleAfterSeconds, LastErrorCode: health.LastErrorCode}
	if !health.CheckedAt.IsZero() {
		out.CheckedAt = health.CheckedAt.UTC().Format(time.RFC3339Nano)
	}
	if !health.LastSuccessAt.IsZero() {
		out.LastSuccessAt = health.LastSuccessAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func (s *server) runManagedPostgresHealthCollector(ctx context.Context) {
	if s.managedPostgresHealthCollector == nil {
		return
	}
	if err := s.managedPostgresHealthCollector.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("managed postgres health collector exited", "error", err)
	}
}
