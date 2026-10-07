package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RequestTelemetryRouteCustomers is a read of retained debugger telemetry,
// independent from the financial consumer usage ledger.
func (s *PgStore) RequestTelemetryRouteCustomers(ctx context.Context, arg sqlc.RequestTelemetryRouteCustomersParams) ([]sqlc.RequestTelemetryRouteCustomersRow, error) {
	return s.appErrorsQueries().RequestTelemetryRouteCustomers(ctx, s.pool, arg)
}

func (m *MemStore) RequestTelemetryRouteCustomers(_ context.Context, _ sqlc.RequestTelemetryRouteCustomersParams) ([]sqlc.RequestTelemetryRouteCustomersRow, error) {
	return nil, errMemStoreRequestTelemetry
}
