// memstore_data_upstreams_mega5_test.go — Coverage Mega-PR #5 cluster 2:
// pin the ADR-098 data_upstreams MemStore stubs
// (pkg/state/memstore_data_upstreams.go) at 100%. Each stub is a
// one-liner that returns the package-private sentinel
// errMemStoreDataUpstreams; the tests assert that calling the stub
// from MemStore surfaces the sentinel, matching the contract the
// file-level comment documents.
//
// Whitebox `package state`. No Postgres dependency — runs on the
// unit-tests-pure-* CI lanes.

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestMemStore_InsertDataUpstream_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	id, err := m.InsertDataUpstream(t.Context(), sqlc.InsertDataUpstreamParams{})
	if id != uuid.Nil {
		t.Errorf("id = %v, want uuid.Nil", id)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}

func TestMemStore_ListDataUpstreamsByApp_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	rows, err := m.ListDataUpstreamsByApp(t.Context(), sqlc.ListDataUpstreamsByAppParams{})
	if rows != nil {
		t.Errorf("rows = %+v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}

func TestMemStore_GetDataUpstreamByID_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	row, err := m.GetDataUpstreamByID(t.Context(), uuid.New())
	if row != (state.DataUpstream{}) {
		t.Errorf("row = %+v, want zero value", row)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}

func TestMemStore_DeleteDataUpstreamByID_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	if err := m.DeleteDataUpstreamByID(t.Context(), uuid.New()); !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}

func TestMemStore_InsertDataUpstreamProbe_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	if err := m.InsertDataUpstreamProbe(t.Context(), sqlc.InsertDataUpstreamProbeParams{}); !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}

func TestMemStore_ListDataUpstreamProbesByHostRegion_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	rows, err := m.ListDataUpstreamProbesByHostRegion(t.Context(), sqlc.ListDataUpstreamProbesByHostRegionParams{})
	if rows != nil {
		t.Errorf("rows = %+v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}

func TestMemStore_PruneDataUpstreamProbesOlderThan_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	if err := m.PruneDataUpstreamProbesOlderThan(t.Context(), time.Now()); !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}

func TestMemStore_ListAllAppDataUpstreams_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	rows, err := m.ListAllAppDataUpstreams(t.Context(), "acct-1", "scope-all")
	if rows != nil {
		t.Errorf("rows = %+v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}

func TestMemStore_CountDataUpstreamsByApp_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	n, err := m.CountDataUpstreamsByApp(t.Context(), "acct-1", "scope-1")
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}

func TestMemStore_ListDistinctUpstreamHostHashes_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	rows, err := m.ListDistinctUpstreamHostHashes(t.Context())
	if rows != nil {
		t.Errorf("rows = %+v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreDataUpstreams)) {
		t.Errorf("err = %v, want errMemStoreDataUpstreams", err)
	}
}
