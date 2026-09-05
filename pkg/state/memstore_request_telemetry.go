// MemStore stubs for ADR-127 production debugger. The production
// path is Postgres via the sqlc-generated queries; MemStore exists
// for unit tests + local development.
//
// Every method here returns either an empty result or a sentinel
// "not implemented" error so the test suite fails loudly if a unit
// test exercises a request-telemetry code path against MemStore
// (the right answer is to mark the test //go:build metal or run it
// against the pgtest harness instead — same posture as
// memstore_app_errors.go for ADR-096 and memstore_app_webhooks.go
// for ADR-091).

package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// errMemStoreRequestTelemetry is the sentinel returned by every
// ADR-127 stub on MemStore. Catching this in a unit test means the
// test should be //go:build metal (run against pgtest) rather than
// MemStore.
var errMemStoreRequestTelemetry = errors.New("state: MemStore does not implement ADR-127 request_telemetry — run the test against pgtest")

// InsertRequestTelemetry (ADR-127 §Decision 1) — MemStore stub.
// Postgres-only.
func (m *MemStore) InsertRequestTelemetry(_ context.Context, _ sqlc.InsertRequestTelemetryParams) error {
	return errMemStoreRequestTelemetry
}

// ListRequestTelemetryByApp (ADR-127 §Decision 1) — MemStore stub.
// Postgres-only. Handlers depending on this query against MemStore
// should be //go:build metal.
func (m *MemStore) ListRequestTelemetryByApp(_ context.Context, _ sqlc.ListRequestTelemetryByAppParams) ([]sqlc.ListRequestTelemetryByAppRow, error) {
	return nil, errMemStoreRequestTelemetry
}

// GetRequestTelemetryByAppAndID (ADR-127) — MemStore stub.
// Postgres-only.
func (m *MemStore) GetRequestTelemetryByAppAndID(_ context.Context, _ sqlc.GetRequestTelemetryByAppAndIDParams) (sqlc.GetRequestTelemetryByAppAndIDRow, error) {
	return sqlc.GetRequestTelemetryByAppAndIDRow{}, errMemStoreRequestTelemetry
}

// RequestTelemetryByDeployment (ADR-127 §Decision 1) — MemStore
// stub. Postgres-only.
func (m *MemStore) RequestTelemetryByDeployment(_ context.Context, _ sqlc.RequestTelemetryByDeploymentParams) ([]sqlc.RequestTelemetryByDeploymentRow, error) {
	return nil, errMemStoreRequestTelemetry
}

// RequestTelemetryBaselineP95ByRoute (ADR-127 PR-B) — MemStore
// stub. Postgres-only.
func (m *MemStore) RequestTelemetryBaselineP95ByRoute(_ context.Context, _ sqlc.RequestTelemetryBaselineP95ByRouteParams) ([]sqlc.RequestTelemetryBaselineP95ByRouteRow, error) {
	return nil, errMemStoreRequestTelemetry
}

// uuid.UUID import retained for consistency with the memstore_app_errors
// stub pattern (future PRs may add an in-memory LRU helper).
var _ = uuid.Nil
