// memstore_request_telemetry_mega5_test.go — Coverage Mega-PR #5
// cluster 3: pin the ADR-127 request_telemetry MemStore stubs
// (pkg/state/memstore_request_telemetry.go) at 100%. Each stub is
// a one-liner that returns the package-private sentinel
// errMemStoreRequestTelemetry; the tests assert that calling the
// stub from MemStore surfaces the sentinel, matching the contract
// the file-level comment documents.
//
// Whitebox `package state`. No Postgres dependency — runs on the
// unit-tests-pure-* CI lanes.

package state

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestMemStore_InsertRequestTelemetry_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	if err := m.InsertRequestTelemetry(t.Context(), sqlc.InsertRequestTelemetryParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("err = %v, want errMemStoreRequestTelemetry", err)
	}
}

func TestMemStore_ListRequestTelemetryByApp_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	rows, err := m.ListRequestTelemetryByApp(t.Context(), sqlc.ListRequestTelemetryByAppParams{})
	if rows != nil {
		t.Errorf("rows = %+v, want nil", rows)
	}
	if !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("err = %v, want errMemStoreRequestTelemetry", err)
	}
}

func TestMemStore_RequestTelemetryCoverage_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	row, err := m.RequestTelemetryCoverage(t.Context(), sqlc.RequestTelemetryCoverageParams{})
	if row != (sqlc.RequestTelemetryCoverageRow{}) {
		t.Errorf("row = %+v, want zero row", row)
	}
	if !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("err = %v, want errMemStoreRequestTelemetry", err)
	}
}

func TestMemStore_RequestTelemetryByDeployment_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	rows, err := m.RequestTelemetryByDeployment(t.Context(), sqlc.RequestTelemetryByDeploymentParams{})
	if rows != nil {
		t.Errorf("rows = %+v, want nil", rows)
	}
	if !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("err = %v, want errMemStoreRequestTelemetry", err)
	}
}

func TestMemStore_RequestTelemetryBaselineP95ByRoute_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	rows, err := m.RequestTelemetryBaselineP95ByRoute(t.Context(), sqlc.RequestTelemetryBaselineP95ByRouteParams{})
	if rows != nil {
		t.Errorf("rows = %+v, want nil", rows)
	}
	if !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("err = %v, want errMemStoreRequestTelemetry", err)
	}
}
