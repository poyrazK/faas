package state_test

// Coverage for the MemStore stub surface on ADR-096 customer-facing
// automatic error grouping (memstore_app_errors.go). Mirrors the
// memstore_request_telemetry_test.go precedent.
//
// These stubs are intentional sentinel-return helpers: the
// production path is Postgres via the sqlc-generated queries
// (table added in migration 00222, drill-down added in 00228).
// MemStore exists for unit tests + local development; a test that
// exercises the error-grouping code path against MemStore fails
// loudly with a "use pgtest" hint instead of silently returning
// empty data.
//
// Coverage of these stubs is the load-bearing driver of the
// pkg/state coverage gate (≥70%): each stub body is 2 lines
// (return + sentinel), and 9 stubs × ~3 statements each = ~27
// statements uncovered if no test pins them. Without this file
// the 69.9%-vs-70% CI variance on the coverage floor would tip
// the gate red on every run.
//
// Test surface — one test per stub family, each verifying the
// sentinel propagates AND the empty-result contract:
//   - IncrementAppError returns (false, sentinel)
//   - InsertAppErrorRequest returns sentinel
//   - ListAppErrorGroups returns (nil, sentinel)
//   - ListAppErrorRequests returns (nil, sentinel)
//   - GetAppErrorSample returns (zero row, sentinel)
//   - ListAppErrorFingerprintsForPurge returns (nil, sentinel)
//   - DeleteAppErrorsByIDs returns sentinel
//   - DeleteAppErrorRequestsByIDs returns sentinel
//   - DeleteAppErrorRequestsOlderThan returns sentinel

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

// TestMemStore_AppErrors_IncrementAppError pins the per-request
// increment path. The stub returns (false, sentinel) so a unit
// test exercising the code path against MemStore fails fast with
// a clear "use pgtest" message instead of silently returning a
// false-zero increment that the test would misread.
func TestMemStore_AppErrors_IncrementAppError(t *testing.T) {
	m := state.NewMemStore()
	ok, err := m.IncrementAppError(context.Background(), sqlc.IncrementAppErrorParams{
		ID:            pgtype.UUID{},
		AccountID:     pgtype.UUID{},
		AppID:         pgtype.UUID{},
		DeploymentID:  pgtype.UUID{},
		Fingerprint:   "fp-1",
		Route:         "GET /foo",
		HttpStatus:    500,
		ErrorClass:    "internal",
		SampleMessage: "boom",
		FirstSeenAt:   pgtype.Timestamptz{},
	})
	if ok {
		t.Errorf("IncrementAppError: ok=true, want false (stub must not advertise a positive increment)")
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("IncrementAppError: err = %v, want errMemStoreAppErrors", err)
	}
}

// TestMemStore_AppErrors_InsertAppErrorRequest pins the per-
// request drill-down row insert.
func TestMemStore_AppErrors_InsertAppErrorRequest(t *testing.T) {
	m := state.NewMemStore()
	if err := m.InsertAppErrorRequest(context.Background(), sqlc.InsertAppErrorRequestParams{}); !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("InsertAppErrorRequest: err = %v, want errMemStoreAppErrors", err)
	}
}

// TestMemStore_AppErrors_ListAppErrorGroups pins the per-app
// groups list read path.
func TestMemStore_AppErrors_ListAppErrorGroups(t *testing.T) {
	m := state.NewMemStore()
	rows, err := m.ListAppErrorGroups(context.Background(), sqlc.ListAppErrorGroupsParams{})
	if rows != nil {
		t.Errorf("ListAppErrorGroups: rows = %v, want nil (empty result contract)", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("ListAppErrorGroups: err = %v, want errMemStoreAppErrors", err)
	}
}

// TestMemStore_AppErrors_ListAppErrorRequests pins the per-
// fingerprint drill-down list read path.
func TestMemStore_AppErrors_ListAppErrorRequests(t *testing.T) {
	m := state.NewMemStore()
	rows, err := m.ListAppErrorRequests(context.Background(), sqlc.ListAppErrorRequestsParams{})
	if rows != nil {
		t.Errorf("ListAppErrorRequests: rows = %v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("ListAppErrorRequests: err = %v, want errMemStoreAppErrors", err)
	}
}

// TestMemStore_AppErrors_GetAppErrorSample pins the per-
// fingerprint sample-message read path. The stub returns the
// zero-value AppErrorSampleRow + sentinel — verifying both
// halves of the contract keeps a future "stub returns nil
// interface" regression honest.
func TestMemStore_AppErrors_GetAppErrorSample(t *testing.T) {
	m := state.NewMemStore()
	row, err := m.GetAppErrorSample(context.Background(), sqlc.GetAppErrorSampleParams{})
	if row.SampleMessage != "" {
		t.Errorf("GetAppErrorSample: row.SampleMessage = %q, want zero value", row.SampleMessage)
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("GetAppErrorSample: err = %v, want errMemStoreAppErrors", err)
	}
}

// TestMemStore_AppErrors_ListAppErrorFingerprintsForPurge pins
// the nightly retention purge read path.
func TestMemStore_AppErrors_ListAppErrorFingerprintsForPurge(t *testing.T) {
	m := state.NewMemStore()
	rows, err := m.ListAppErrorFingerprintsForPurge(context.Background(), sqlc.ListAppErrorFingerprintsForPurgeParams{})
	if rows != nil {
		t.Errorf("ListAppErrorFingerprintsForPurge: rows = %v, want nil", rows)
	}
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("ListAppErrorFingerprintsForPurge: err = %v, want errMemStoreAppErrors", err)
	}
}

// TestMemStore_AppErrors_DeleteAppErrorsByIDs pins the per-app
// retention delete path. Even though the stub doesn't take a
// payload that matters (returns sentinel), exercising it pins the
// method body.
func TestMemStore_AppErrors_DeleteAppErrorsByIDs(t *testing.T) {
	m := state.NewMemStore()
	err := m.DeleteAppErrorsByIDs(context.Background(), []uuid.UUID{uuid.New(), uuid.New()})
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("DeleteAppErrorsByIDs: err = %v, want errMemStoreAppErrors", err)
	}
}

// TestMemStore_AppErrors_DeleteAppErrorRequestsByIDs pins the
// per-fingerprint drill-down retention delete path.
func TestMemStore_AppErrors_DeleteAppErrorRequestsByIDs(t *testing.T) {
	m := state.NewMemStore()
	err := m.DeleteAppErrorRequestsByIDs(context.Background(), []uuid.UUID{uuid.New()})
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("DeleteAppErrorRequestsByIDs: err = %v, want errMemStoreAppErrors", err)
	}
}

// TestMemStore_AppErrors_DeleteAppErrorRequestsOlderThan pins
// the cursor-driven retention delete path.
func TestMemStore_AppErrors_DeleteAppErrorRequestsOlderThan(t *testing.T) {
	m := state.NewMemStore()
	err := m.DeleteAppErrorRequestsOlderThan(context.Background(), uuid.New(), time.Now())
	if !errors.Is(err, (*state.ErrMemStoreAppErrors)) {
		t.Errorf("DeleteAppErrorRequestsOlderThan: err = %v, want errMemStoreAppErrors", err)
	}
}
