// memstore_app_errors_mega5_test.go — Coverage Mega-PR #5 cluster 1:
// pin the ADR-096 app_errors MemStore stubs (pkg/state/memstore_app_errors.go)
// at 100%. Each stub is a one-liner that returns the package-private
// sentinel errMemStoreAppErrors; the tests assert that calling the
// stub from MemStore surfaces the sentinel, matching the contract
// the file-level comment documents ("the right answer is to mark the
// test //go:build metal or run it against the pgtest harness
// instead").
//
// Whitebox `package state`. No Postgres dependency — runs on the
// unit-tests-pure-* CI lanes.

package state_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestMemStore_IncrementAppError_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	ok, err := m.IncrementAppError(t.Context(), sqlc.IncrementAppErrorParams{})
	if ok {
		t.Error("ok = true, want false")
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("err = %v, want errMemStoreAppErrors", err)
	}
}

func TestMemStore_InsertAppErrorRequest_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	if err := m.InsertAppErrorRequest(t.Context(), sqlc.InsertAppErrorRequestParams{}); !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("err = %v, want errMemStoreAppErrors", err)
	}
}

func TestMemStore_ListAppErrorGroups_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	rows, err := m.ListAppErrorGroups(t.Context(), sqlc.ListAppErrorGroupsParams{})
	if rows != nil {
		t.Errorf("rows = %+v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("err = %v, want errMemStoreAppErrors", err)
	}
}

func TestMemStore_ListAppErrorRequests_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	rows, err := m.ListAppErrorRequests(t.Context(), sqlc.ListAppErrorRequestsParams{})
	if rows != nil {
		t.Errorf("rows = %+v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("err = %v, want errMemStoreAppErrors", err)
	}
}

func TestMemStore_GetAppErrorSample_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	row, err := m.GetAppErrorSample(t.Context(), sqlc.GetAppErrorSampleParams{})
	if !reflect.DeepEqual(row, state.AppErrorSampleRow{}) {
		t.Errorf("row = %+v, want zero value", row)
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("err = %v, want errMemStoreAppErrors", err)
	}
}

func TestMemStore_ListAppErrorFingerprintsForPurge_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	ids, err := m.ListAppErrorFingerprintsForPurge(t.Context(), sqlc.ListAppErrorFingerprintsForPurgeParams{})
	if ids != nil {
		t.Errorf("ids = %+v, want nil", ids)
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("err = %v, want errMemStoreAppErrors", err)
	}
}

func TestMemStore_DeleteAppErrorsByIDs_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	ids := []uuid.UUID{uuid.New()}
	if err := m.DeleteAppErrorsByIDs(t.Context(), ids); !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("err = %v, want errMemStoreAppErrors", err)
	}
}

func TestMemStore_DeleteAppErrorRequestsByIDs_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	ids := []uuid.UUID{uuid.New()}
	if err := m.DeleteAppErrorRequestsByIDs(t.Context(), ids); !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("err = %v, want errMemStoreAppErrors", err)
	}
}

func TestMemStore_DeleteAppErrorRequestsOlderThan_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	if err := m.DeleteAppErrorRequestsOlderThan(t.Context(), uuid.Nil, time.Now()); !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("err = %v, want errMemStoreAppErrors", err)
	}
}
