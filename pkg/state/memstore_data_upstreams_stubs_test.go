package state_test

// Coverage for the MemStore stub surface on ADR-098
// connection-aware execution (memstore_data_upstreams.go).
// Mirrors memstore_app_errors_stubs_test.go precedent.
//
// All 10 stubs return errMemStoreDataUpstreams. Each test pins
// the sentinel propagation AND the empty-result contract:
//
//   - InsertDataUpstream → (uuid.Nil, sentinel)
//   - ListDataUpstreamsByApp → (nil, sentinel)
//   - GetDataUpstreamByID → (zero row, sentinel)
//   - DeleteDataUpstreamByID → sentinel
//   - InsertDataUpstreamProbe → sentinel
//   - ListDataUpstreamProbesByHostRegion → (nil, sentinel)
//   - PruneDataUpstreamProbesOlderThan → sentinel
//   - ListAllAppDataUpstreams → (nil, sentinel)
//   - CountDataUpstreamsByApp → (0, sentinel)
//   - ListDistinctUpstreamHostHashes → (nil, sentinel)
//
// Verifying both halves of the (result, error) contract keeps
// a future "stub returns nil interface" regression honest —
// callers that ignore the err and inspect the result get a
// zero value, but callers that handle err see the sentinel and
// fail loudly with a "use pgtest" hint.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TestMemStore_DataUpstreams_InsertDataUpstream pins the
// per-app upstream INSERT path.
func TestMemStore_DataUpstreams_InsertDataUpstream(t *testing.T) {
	m := state.NewMemStore()
	id, err := m.InsertDataUpstream(context.Background(), sqlc.InsertDataUpstreamParams{
		ID:               pgtype.UUID{},
		AccountID:        pgtype.UUID{},
		AppID:            pgtype.UUID{},
		Source:           "manual",
		Scope:            "app",
		DeploymentScope:  "",
		Kind:             "https",
		Host:             "api.example.com",
		Port:             443,
		HostRedactedHash: "abc123",
		DeclaredRegion:   pgtype.Text{String: "eu-west-1", Valid: true},
		LastRttMs:        pgtype.Int4{},
		LastProbedAt:     pgtype.Timestamptz{},
	})
	if id != uuid.Nil {
		t.Errorf("InsertDataUpstream: id = %v, want uuid.Nil (empty-result contract)", id)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("InsertDataUpstream: err = %v, want errMemStoreDataUpstreams", err)
	}
}

// TestMemStore_DataUpstreams_ListDataUpstreamsByApp pins the
// per-app LIST path.
func TestMemStore_DataUpstreams_ListDataUpstreamsByApp(t *testing.T) {
	m := state.NewMemStore()
	rows, err := m.ListDataUpstreamsByApp(context.Background(), sqlc.ListDataUpstreamsByAppParams{
		AppID:                 pgtype.UUID{},
		CursorDeploymentScope: "",
		CursorCreatedAt:       pgtype.Timestamptz{},
		CursorID:              pgtype.UUID{},
		PageLimit:             50,
	})
	if rows != nil {
		t.Errorf("ListDataUpstreamsByApp: rows = %v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("ListDataUpstreamsByApp: err = %v, want errMemStoreDataUpstreams", err)
	}
}

// TestMemStore_DataUpstreams_GetDataUpstreamByID pins the
// per-upstream GET path.
func TestMemStore_DataUpstreams_GetDataUpstreamByID(t *testing.T) {
	m := state.NewMemStore()
	row, err := m.GetDataUpstreamByID(context.Background(), uuid.New())
	if row.Host != "" {
		t.Errorf("GetDataUpstreamByID: row.Host = %q, want zero value", row.Host)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("GetDataUpstreamByID: err = %v, want errMemStoreDataUpstreams", err)
	}
}

// TestMemStore_DataUpstreams_DeleteDataUpstreamByID pins the
// per-upstream DELETE path.
func TestMemStore_DataUpstreams_DeleteDataUpstreamByID(t *testing.T) {
	m := state.NewMemStore()
	err := m.DeleteDataUpstreamByID(context.Background(), uuid.New())
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("DeleteDataUpstreamByID: err = %v, want errMemStoreDataUpstreams", err)
	}
}

// TestMemStore_DataUpstreams_InsertDataUpstreamProbe pins the
// per-probe INSERT path used by meterd's probe loop.
func TestMemStore_DataUpstreams_InsertDataUpstreamProbe(t *testing.T) {
	m := state.NewMemStore()
	err := m.InsertDataUpstreamProbe(context.Background(), sqlc.InsertDataUpstreamProbeParams{
		ID:               pgtype.UUID{},
		HostRedactedHash: "abc",
		Region:           "eu-west-1",
		Kind:             "https",
		SampledAt:        pgtype.Timestamptz{},
	})
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("InsertDataUpstreamProbe: err = %v, want errMemStoreDataUpstreams", err)
	}
}

// TestMemStore_DataUpstreams_ListDataUpstreamProbesByHostRegion
// pins the per-host/region probe LIST path used by the dashboard
// upstream-latency widget.
func TestMemStore_DataUpstreams_ListDataUpstreamProbesByHostRegion(t *testing.T) {
	m := state.NewMemStore()
	rows, err := m.ListDataUpstreamProbesByHostRegion(context.Background(), sqlc.ListDataUpstreamProbesByHostRegionParams{
		HostRedactedHash: "abc",
		Region:           "eu-west-1",
		SampledAt:        pgtype.Timestamptz{},
		Limit:            50,
	})
	if rows != nil {
		t.Errorf("ListDataUpstreamProbesByHostRegion: rows = %v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("ListDataUpstreamProbesByHostRegion: err = %v, want errMemStoreDataUpstreams", err)
	}
}

// TestMemStore_DataUpstreams_PruneDataUpstreamProbesOlderThan
// pins the nightly retention PRUNE path.
func TestMemStore_DataUpstreams_PruneDataUpstreamProbesOlderThan(t *testing.T) {
	m := state.NewMemStore()
	err := m.PruneDataUpstreamProbesOlderThan(context.Background(), time.Now().Add(-24*time.Hour))
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("PruneDataUpstreamProbesOlderThan: err = %v, want errMemStoreDataUpstreams", err)
	}
}

// TestMemStore_DataUpstreams_ListAllAppDataUpstreams pins the
// account-wide LIST path used by GET /v1/apps/{slug}/upstreams?
// scope=__all__ (PR-B scope flag).
func TestMemStore_DataUpstreams_ListAllAppDataUpstreams(t *testing.T) {
	m := state.NewMemStore()
	rows, err := m.ListAllAppDataUpstreams(context.Background(), "acct-id", "scope-all")
	if rows != nil {
		t.Errorf("ListAllAppDataUpstreams: rows = %v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("ListAllAppDataUpstreams: err = %v, want errMemStoreDataUpstreams", err)
	}
}

// TestMemStore_DataUpstreams_CountDataUpstreamsByApp pins the
// per-app COUNT path used by createUpstream's per-plan cap
// admission (PR-B).
func TestMemStore_DataUpstreams_CountDataUpstreamsByApp(t *testing.T) {
	m := state.NewMemStore()
	n, err := m.CountDataUpstreamsByApp(context.Background(), "acct-id", "app-slug")
	if n != 0 {
		t.Errorf("CountDataUpstreamsByApp: n = %d, want 0 (empty-result contract)", n)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("CountDataUpstreamsByApp: err = %v, want errMemStoreDataUpstreams", err)
	}
}

// TestMemStore_DataUpstreams_ListDistinctUpstreamHostHashes
// pins the meterd probe loop's distinct-host enumeration path
// (PR-C). The stub returns (nil, sentinel); the meterd sweep
// in unit tests fails fast on the sentinel rather than silently
// skipping probe work.
func TestMemStore_DataUpstreams_ListDistinctUpstreamHostHashes(t *testing.T) {
	m := state.NewMemStore()
	rows, err := m.ListDistinctUpstreamHostHashes(context.Background())
	if rows != nil {
		t.Errorf("ListDistinctUpstreamHostHashes: rows = %v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("ListDistinctUpstreamHostHashes: err = %v, want errMemStoreDataUpstreams", err)
	}
}
