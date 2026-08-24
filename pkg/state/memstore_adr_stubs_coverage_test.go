// Regression coverage for the three ADR-x MemStore stub files whose
// bodies (single-line sentinel returns) were 0%-covered on the latest
// pg-shard-2 CI run, dragging pkg/state coverage from 70.0% to 69.9%.
//
// Each method here returns either an empty result or a sentinel
// "not implemented" error; calling the method and asserting
// errors.Is(err, sentinel) is a well-typed exercise of every body
// line. The same shape as memstore_alert_rules_test.go and the
// existing memstore_app_webhooks_test.go: no Postgres, no fixture —
// just a MemStore{} and the documented sentinels.
//
// Pattern follows PR #1064 (round-30) and PR #1067 (round-5) which
// used the same approach to push pkg/state back above the 70% gate
// when prior ADR work nudged it below.

package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TestMemStore_AppErrors_StubsReturnSentinel exercises every method
// in memstore_app_errors.go (ADR-096) and pins the errMemStoreAppErrors
// sentinel. Without this test the package-level gate flirts with
// 69.9% on a marginally different shard scheduling.
func TestMemStore_AppErrors_StubsReturnSentinel(t *testing.T) {
	m := &MemStore{}
	ctx := context.Background()

	if _, err := m.IncrementAppError(ctx, sqlc.IncrementAppErrorParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("IncrementAppError: got %v, want errors.Is(_, errMemStoreAppErrors)", err)
	}
	if err := m.InsertAppErrorRequest(ctx, sqlc.InsertAppErrorRequestParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("InsertAppErrorRequest: got %v, want errors.Is(_, errMemStoreAppErrors)", err)
	}
	if _, err := m.ListAppErrorGroups(ctx, sqlc.ListAppErrorGroupsParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("ListAppErrorGroups: got %v, want errors.Is(_, errMemStoreAppErrors)", err)
	}
	if _, err := m.ListAppErrorRequests(ctx, sqlc.ListAppErrorRequestsParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("ListAppErrorRequests: got %v, want errors.Is(_, errMemStoreAppErrors)", err)
	}
	if _, err := m.GetAppErrorSample(ctx, sqlc.GetAppErrorSampleParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("GetAppErrorSample: got %v, want errors.Is(_, errMemStoreAppErrors)", err)
	}
	if _, err := m.ListAppErrorFingerprintsForPurge(ctx, sqlc.ListAppErrorFingerprintsForPurgeParams{}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("ListAppErrorFingerprintsForPurge: got %v, want errors.Is(_, errMemStoreAppErrors)", err)
	}
	if err := m.DeleteAppErrorsByIDs(ctx, []uuid.UUID{uuid.New()}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("DeleteAppErrorsByIDs: got %v, want errors.Is(_, errMemStoreAppErrors)", err)
	}
	if err := m.DeleteAppErrorRequestsByIDs(ctx, []uuid.UUID{uuid.New()}); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("DeleteAppErrorRequestsByIDs: got %v, want errors.Is(_, errMemStoreAppErrors)", err)
	}
	if err := m.DeleteAppErrorRequestsOlderThan(ctx, uuid.New(), time.Now()); !errors.Is(err, errMemStoreAppErrors) {
		t.Errorf("DeleteAppErrorRequestsOlderThan: got %v, want errors.Is(_, errMemStoreAppErrors)", err)
	}
}

// TestMemStore_DataUpstreams_StubsReturnSentinel exercises every
// method in memstore_data_upstreams.go (ADR-098 connection-aware
// execution).
func TestMemStore_DataUpstreams_StubsReturnSentinel(t *testing.T) {
	m := &MemStore{}
	ctx := context.Background()

	if _, err := m.InsertDataUpstream(ctx, sqlc.InsertDataUpstreamParams{}); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("InsertDataUpstream: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
	if _, err := m.ListDataUpstreamsByApp(ctx, sqlc.ListDataUpstreamsByAppParams{}); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("ListDataUpstreamsByApp: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
	if _, err := m.GetDataUpstreamByID(ctx, uuid.New()); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("GetDataUpstreamByID: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
	if err := m.DeleteDataUpstreamByID(ctx, uuid.New()); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("DeleteDataUpstreamByID: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
	if err := m.InsertDataUpstreamProbe(ctx, sqlc.InsertDataUpstreamProbeParams{}); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("InsertDataUpstreamProbe: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
	if _, err := m.ListDataUpstreamProbesByHostRegion(ctx, sqlc.ListDataUpstreamProbesByHostRegionParams{}); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("ListDataUpstreamProbesByHostRegion: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
	if err := m.PruneDataUpstreamProbesOlderThan(ctx, time.Now()); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("PruneDataUpstreamProbesOlderThan: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
	if _, err := m.ListAllAppDataUpstreams(ctx, "acct", "app"); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("ListAllAppDataUpstreams: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
	if _, err := m.CountDataUpstreamsByApp(ctx, "acct", "app"); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("CountDataUpstreamsByApp: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
	if _, err := m.ListDistinctUpstreamHostHashes(ctx); !errors.Is(err, errMemStoreDataUpstreams) {
		t.Errorf("ListDistinctUpstreamHostHashes: got %v, want errors.Is(_, errMemStoreDataUpstreams)", err)
	}
}

// TestMemStore_RequestTelemetry_StubsReturnSentinel exercises every
// method in memstore_request_telemetry.go (ADR-127 production
// debugger).
func TestMemStore_RequestTelemetry_StubsReturnSentinel(t *testing.T) {
	m := &MemStore{}
	ctx := context.Background()

	if err := m.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("InsertRequestTelemetry: got %v, want errors.Is(_, errMemStoreRequestTelemetry)", err)
	}
	if _, err := m.ListRequestTelemetryByApp(ctx, sqlc.ListRequestTelemetryByAppParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("ListRequestTelemetryByApp: got %v, want errors.Is(_, errMemStoreRequestTelemetry)", err)
	}
	if _, err := m.RequestTelemetryByDeployment(ctx, sqlc.RequestTelemetryByDeploymentParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("RequestTelemetryByDeployment: got %v, want errors.Is(_, errMemStoreRequestTelemetry)", err)
	}
	if _, err := m.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{}); !errors.Is(err, errMemStoreRequestTelemetry) {
		t.Errorf("RequestTelemetryBaselineP95ByRoute: got %v, want errors.Is(_, errMemStoreRequestTelemetry)", err)
	}
}
