// MemStore stubs for ADR-096 customer-facing automatic error
// grouping. The production path is Postgres via the sqlc-generated
// queries; MemStore exists for unit tests + local development.
//
// Every method here returns either an empty result or a sentinel
// "not implemented" error so the test suite fails loudly if a
// unit test exercises an error-grouping code path against
// MemStore (the right answer is to mark the test //go:build metal
// or run it against the pgtest harness instead — same posture the
// other ADR-091 obs-backend stubs take in memstore_app_webhooks.go
// and memstore_compute_nodes.go).

package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// errMemStoreAppErrors is the sentinel returned by every ADR-096
// stub on MemStore. Catching this in a unit test means the test
// should be //go:build metal (run against pgtest) rather than
// MemStore.
var errMemStoreAppErrors = errors.New("state: MemStore does not implement ADR-096 app_errors — run the test against pgtest")

// IncrementAppError (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) IncrementAppError(_ context.Context, _ sqlc.IncrementAppErrorParams) (bool, error) {
	return false, errMemStoreAppErrors
}

// InsertAppErrorRequest (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) InsertAppErrorRequest(_ context.Context, _ sqlc.InsertAppErrorRequestParams) error {
	return errMemStoreAppErrors
}

// ListAppErrorGroups (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) ListAppErrorGroups(_ context.Context, _ sqlc.ListAppErrorGroupsParams) ([]AppErrorGroup, error) {
	return nil, errMemStoreAppErrors
}

// ListAppErrorRequests (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) ListAppErrorRequests(_ context.Context, _ sqlc.ListAppErrorRequestsParams) ([]AppErrorRequestRow, error) {
	return nil, errMemStoreAppErrors
}

// GetAppErrorSample (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) GetAppErrorSample(_ context.Context, _ sqlc.GetAppErrorSampleParams) (AppErrorSampleRow, error) {
	return AppErrorSampleRow{}, errMemStoreAppErrors
}

// ListAppErrorFingerprintsForPurge (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) ListAppErrorFingerprintsForPurge(_ context.Context, _ sqlc.ListAppErrorFingerprintsForPurgeParams) ([]uuid.UUID, error) {
	return nil, errMemStoreAppErrors
}

// DeleteAppErrorsByIDs (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) DeleteAppErrorsByIDs(_ context.Context, _ []uuid.UUID) error {
	return errMemStoreAppErrors
}

// DeleteAppErrorRequestsByIDs (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) DeleteAppErrorRequestsByIDs(_ context.Context, _ []uuid.UUID) error {
	return errMemStoreAppErrors
}

// DeleteAppErrorRequestsOlderThan (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) DeleteAppErrorRequestsOlderThan(_ context.Context, _ uuid.UUID, _ time.Time) error {
	return errMemStoreAppErrors
}

// CountAppErrorRequestsForFingerprint (ADR-096) — MemStore stub. Postgres-only.
func (m *MemStore) CountAppErrorRequestsForFingerprint(_ context.Context, _ sqlc.CountAppErrorRequestsForFingerprintParams) (int64, error) {
	return 0, errMemStoreAppErrors
}
