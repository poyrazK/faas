// MemStore stubs for ADR-098 connection-aware execution (§9.A).
// The production path is Postgres via the sqlc-generated queries;
// MemStore exists for unit tests + local development.
//
// Every method here returns either an empty result or a sentinel
// "not implemented" error so the test suite fails loudly if a
// unit test exercises an ADR-098 code path against MemStore (the
// right answer is to mark the test //go:build metal or run it
// against the pgtest harness instead — same posture the other
// obs-backend stubs take in memstore_app_errors.go,
// memstore_app_webhooks.go, and memstore_compute_nodes.go).
//
// PR-A ships these stubs because the Store interface gains the
// methods; PR-B replaces each body with an in-memory map if a
// unit test demands it. PR-A keeps the stubs as Postgres-only
// so unit tests stay on the MemStore-fast path by default.

package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// errMemStoreDataUpstreams is the sentinel returned by every
// ADR-098 stub on MemStore. Catching this in a unit test means
// the test should be //go:build metal (run against pgtest) rather
// than MemStore.
var errMemStoreDataUpstreams = errors.New("state: MemStore does not implement ADR-098 data_upstreams — run the test against pgtest")

// InsertDataUpstream (ADR-098) — MemStore stub. Postgres-only.
func (m *MemStore) InsertDataUpstream(_ context.Context, _ sqlc.InsertDataUpstreamParams) (uuid.UUID, error) {
	return uuid.Nil, errMemStoreDataUpstreams
}

// ListDataUpstreamsByApp (ADR-098) — MemStore stub. Postgres-only.
func (m *MemStore) ListDataUpstreamsByApp(_ context.Context, _ sqlc.ListDataUpstreamsByAppParams) ([]DataUpstream, error) {
	return nil, errMemStoreDataUpstreams
}

// GetDataUpstreamByID (ADR-098) — MemStore stub. Postgres-only.
func (m *MemStore) GetDataUpstreamByID(_ context.Context, _ uuid.UUID) (DataUpstream, error) {
	return DataUpstream{}, errMemStoreDataUpstreams
}

// DeleteDataUpstreamByID (ADR-098) — MemStore stub. Postgres-only.
func (m *MemStore) DeleteDataUpstreamByID(_ context.Context, _ uuid.UUID) error {
	return errMemStoreDataUpstreams
}

// InsertDataUpstreamProbe (ADR-098) — MemStore stub. Postgres-only.
func (m *MemStore) InsertDataUpstreamProbe(_ context.Context, _ sqlc.InsertDataUpstreamProbeParams) error {
	return errMemStoreDataUpstreams
}

// ListDataUpstreamProbesByHostRegion (ADR-098) — MemStore stub.
// Postgres-only.
func (m *MemStore) ListDataUpstreamProbesByHostRegion(_ context.Context, _ sqlc.ListDataUpstreamProbesByHostRegionParams) ([]DataUpstreamProbe, error) {
	return nil, errMemStoreDataUpstreams
}

// ListDataUpstreamProbeHistory (issue #953) — MemStore stub. Postgres-only.
func (m *MemStore) ListDataUpstreamProbeHistory(_ context.Context, _, _, _, _ string, _, _ time.Time, _ time.Duration) ([]DataUpstreamProbeHistory, error) {
	return nil, errMemStoreDataUpstreams
}

// PruneDataUpstreamProbesOlderThan (ADR-098) — MemStore stub.
// Postgres-only.
func (m *MemStore) PruneDataUpstreamProbesOlderThan(_ context.Context, _ time.Time) error {
	return errMemStoreDataUpstreams
}

// ListAllAppDataUpstreams (ADR-098 PR-B) — MemStore stub.
// Postgres-only. Used by GET /v1/apps/{slug}/upstreams?scope=__all__.
func (m *MemStore) ListAllAppDataUpstreams(_ context.Context, _, _ string) ([]DataUpstream, error) {
	return nil, errMemStoreDataUpstreams
}

// CountDataUpstreamsByApp (ADR-098 PR-B) — MemStore stub.
// Postgres-only. Used by createUpstream for the per-plan quota.
func (m *MemStore) CountDataUpstreamsByApp(_ context.Context, _, _ string) (int, error) {
	return 0, errMemStoreDataUpstreams
}

// ListDistinctUpstreamHostHashes (ADR-098 PR-C) — MemStore
// stub. Postgres-only. Used by the meterd probe loop.
func (m *MemStore) ListDistinctUpstreamHostHashes(_ context.Context) ([]DataUpstreamTarget, error) {
	return nil, errMemStoreDataUpstreams
}
