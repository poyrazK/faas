// Package state — MemStore ADR-stub coverage pin.
//
// The pkg/state check-state-coverage CI gate floors at 70% on the
// pg-shard-2 job (where PgStore runs against a real Postgres).
// Three ADR stub files on MemStore — ADR-096 app_errors, ADR-098
// data_upstreams, ADR-127 request_telemetry — are Postgres-only
// by design and have no callers in the unit test suite, so the
// gate trips at 69.9% (awk %.1f rounds 69.96%). This file
// exercises every stub method body so the gate stays above 70%
// (currently 70.1%).
//
// Each method returns a sentinel error. The tests assert
// errors.Is(err, errMemStore<ADR>) so a future refactor that
// silently swaps the sentinel for a generic pgx error trips
// here.
//
// Pattern mirrors PR #1064 (UpsertAppOpenAPIDocIfUnderQuota
// coverage pin) and PR #1088 (this exact failure).
package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TestMemStore_AppErrorsStubsCovered (ADR-096) — exercises every
// MemStore stub on memstore_app_errors.go so the pkg/state
// coverage gate stays above 70%. All nine methods must return
// errMemStoreAppErrors.
func TestMemStore_AppErrorsStubsCovered(t *testing.T) {
	m := &MemStore{}
	ctx := context.Background()

	// IncrementAppError.
	if _, err := m.IncrementAppError(ctx, sqlc.IncrementAppErrorParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("IncrementAppError: errors.Is(err, errMemStoreAppErrors) = false; err = %v", err)
	}

	// InsertAppErrorRequest.
	if err := m.InsertAppErrorRequest(ctx, sqlc.InsertAppErrorRequestParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("InsertAppErrorRequest: errors.Is(err, errMemStoreAppErrors) = false; err = %v", err)
	}

	// ListAppErrorGroups.
	if _, err := m.ListAppErrorGroups(ctx, sqlc.ListAppErrorGroupsParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("ListAppErrorGroups: errors.Is(err, errMemStoreAppErrors) = false; err = %v", err)
	}

	// ListAppErrorRequests.
	if _, err := m.ListAppErrorRequests(ctx, sqlc.ListAppErrorRequestsParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("ListAppErrorRequests: errors.Is(err, errMemStoreAppErrors) = false; err = %v", err)
	}

	// GetAppErrorSample.
	if _, err := m.GetAppErrorSample(ctx, sqlc.GetAppErrorSampleParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("GetAppErrorSample: errors.Is(err, errMemStoreAppErrors) = false; err = %v", err)
	}

	// ListAppErrorFingerprintsForPurge.
	if _, err := m.ListAppErrorFingerprintsForPurge(ctx, sqlc.ListAppErrorFingerprintsForPurgeParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("ListAppErrorFingerprintsForPurge: errors.Is(err, errMemStoreAppErrors) = false; err = %v", err)
	}

	// DeleteAppErrorsByIDs.
	if err := m.DeleteAppErrorsByIDs(ctx, nil); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("DeleteAppErrorsByIDs: errors.Is(err, errMemStoreAppErrors) = false; err = %v", err)
	}

	// DeleteAppErrorRequestsByIDs.
	if err := m.DeleteAppErrorRequestsByIDs(ctx, nil); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("DeleteAppErrorRequestsByIDs: errors.Is(err, errMemStoreAppErrors) = false; err = %v", err)
	}

	// DeleteAppErrorRequestsOlderThan.
	if err := m.DeleteAppErrorRequestsOlderThan(ctx, uuid.Nil, time.Time{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("DeleteAppErrorRequestsOlderThan: errors.Is(err, errMemStoreAppErrors) = false; err = %v", err)
	}
}

// TestMemStore_DataUpstreamsStubsCovered (ADR-098) — exercises
// every MemStore stub on memstore_data_upstreams.go. All ten
// methods must return errMemStoreDataUpstreams.
func TestMemStore_DataUpstreamsStubsCovered(t *testing.T) {
	m := &MemStore{}
	ctx := context.Background()

	// InsertDataUpstream.
	if _, err := m.InsertDataUpstream(ctx, sqlc.InsertDataUpstreamParams{}); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("InsertDataUpstream: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}

	// ListDataUpstreamsByApp.
	if _, err := m.ListDataUpstreamsByApp(ctx, sqlc.ListDataUpstreamsByAppParams{}); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("ListDataUpstreamsByApp: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}

	// GetDataUpstreamByID.
	if _, err := m.GetDataUpstreamByID(ctx, uuid.Nil); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("GetDataUpstreamByID: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}

	// DeleteDataUpstreamByID.
	if err := m.DeleteDataUpstreamByID(ctx, uuid.Nil); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("DeleteDataUpstreamByID: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}

	// InsertDataUpstreamProbe.
	if err := m.InsertDataUpstreamProbe(ctx, sqlc.InsertDataUpstreamProbeParams{}); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("InsertDataUpstreamProbe: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}

	// ListDataUpstreamProbesByHostRegion.
	if _, err := m.ListDataUpstreamProbesByHostRegion(ctx, sqlc.ListDataUpstreamProbesByHostRegionParams{}); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("ListDataUpstreamProbesByHostRegion: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}

	// PruneDataUpstreamProbesOlderThan.
	if err := m.PruneDataUpstreamProbesOlderThan(ctx, time.Time{}); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("PruneDataUpstreamProbesOlderThan: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}

	// ListAllAppDataUpstreams.
	if _, err := m.ListAllAppDataUpstreams(ctx, "", ""); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("ListAllAppDataUpstreams: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}

	// CountDataUpstreamsByApp.
	if _, err := m.CountDataUpstreamsByApp(ctx, "", ""); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("CountDataUpstreamsByApp: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}

	// ListDistinctUpstreamHostHashes.
	if _, err := m.ListDistinctUpstreamHostHashes(ctx); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("ListDistinctUpstreamHostHashes: errors.Is(err, errMemStoreDataUpstreams) = false; err = %v", err)
	}
}

// TestMemStore_RequestTelemetryStubsCovered (ADR-127) — exercises
// every MemStore stub on memstore_request_telemetry.go. Each method
// must return errMemStoreRequestTelemetry.
func TestMemStore_RequestTelemetryStubsCovered(t *testing.T) {
	m := &MemStore{}
	ctx := context.Background()

	// InsertRequestTelemetry.
	if err := m.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("InsertRequestTelemetry: errors.Is(err, errMemStoreRequestTelemetry) = false; err = %v", err)
	}

	// ListRequestTelemetryByApp.
	if _, err := m.ListRequestTelemetryByApp(ctx, sqlc.ListRequestTelemetryByAppParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("ListRequestTelemetryByApp: errors.Is(err, errMemStoreRequestTelemetry) = false; err = %v", err)
	}

	// RequestTelemetryCoverage.
	if _, err := m.RequestTelemetryCoverage(ctx, sqlc.RequestTelemetryCoverageParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("RequestTelemetryCoverage: errors.Is(err, errMemStoreRequestTelemetry) = false; err = %v", err)
	}

	// RequestTelemetryByDeployment.
	if _, err := m.RequestTelemetryByDeployment(ctx, sqlc.RequestTelemetryByDeploymentParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("RequestTelemetryByDeployment: errors.Is(err, errMemStoreRequestTelemetry) = false; err = %v", err)
	}

	// RequestTelemetryBaselineP95ByRoute.
	if _, err := m.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("RequestTelemetryBaselineP95ByRoute: errors.Is(err, errMemStoreRequestTelemetry) = false; err = %v", err)
	}
}
