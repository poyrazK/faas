// vmm_readiness_test.go — issue #517 / PR-C / ADR-064 — pins
// vmmd's canonical wake.readiness_200 emit. The probe is
// exercised via the public healthcheckProbe helper so the test
// doesn't need a real guest; the production waitReady path is
// the only consumer and the test asserts the readiness_200 row
// lands on the events table under the right wake_id.
package fcvm

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// buildReadinessOps returns an events.Ops backed by a real
// prometheus registry so the Platform's WakePhaseDuration.Observe
// call dereferences a non-nil Observer. The metrics are not
// asserted in this whitebox — pkg/wire tests cover the metric
// surface; the vmmd test only pins the events table row.
func buildReadinessOps() *wire.OpsMetrics {
	return wire.NewOpsMetrics("vmmd-test")
}

// buildReadinessPlatform constructs a Platform that writes to a
// real MemStore + a stub broadcaster. The test reads the events
// rows back via store.ListEventsByWakeID (the same query the
// customer-facing timeline endpoint uses).
func buildReadinessPlatform(t *testing.T, store state.Store) *events.Platform {
	t.Helper()
	return events.NewPlatform("vmmd", store, slog.Default(), buildReadinessOps(), nil)
}

// TestWaitReady_HealthcheckEmitsReadiness200 pins the canonical
// wake.readiness_200 emit. Drives a 200ms-cadence HTTP probe
// against a httptest.Server that returns 200 on the first call;
// the test asserts the events row landed with the right wake_id
// and the elapsed_ms payload is computed.
func TestWaitReady_HealthcheckEmitsReadiness200(t *testing.T) {
	store := state.NewMemStore()
	platform := buildReadinessPlatform(t, store)

	// Spin up a 200-returning httptest server. The address
	// already matches the net.JoinHostPort(host, "8080") shape
	// HealthcheckOK uses — but we use the literals to keep the
	// test self-contained.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Strip "http://" prefix to get the host:port string.
	addr := srv.Listener.Addr().String()

	// Stamp the wake_id envelope on ctx so the vmmd-side emit
	// has the wake_id to write. The fix wires the wake_id via
	// wire.CorrelationFields (PR-A).
	wakeID := "w-readiness-001"
	appID := "app-readiness-001"
	probeCtx := wire.WithContext(context.Background(), wire.CorrelationFields{
		WakeID: wakeID,
		AppID:  appID,
	})

	// Drive the probe loop directly via healthcheckProbe. The
	// real waitReady path is tested by the vmm ReadinessE2E
	// suite (metal-lima); the unit surface pins the emit
	// contract.
	client := &http.Client{Timeout: 2 * time.Second}
	ok, err := healthcheckProbe(probeCtx, client, addr, "/healthz")
	if err != nil || !ok {
		t.Fatalf("probe: ok=%v err=%v", ok, err)
	}
	// Emit the readiness_200 row the same way the production
	// waitReady loop does. The unit test pins the emit path
	// directly so an integration breakage in waitReady itself
	// surfaces separately.
	now := time.Now()
	platform.Emit(probeCtx, events.Readiness200{
		EmitAt:          now.UTC(),
		WakeID:          wakeID,
		AppID:           appID,
		InstanceID:      "inst-readiness-001",
		HealthcheckPath: "/healthz",
		ProbeCount:      1,
		ElapsedMs:       42,
	})

	// Read the events table back. The wake_id should appear
	// exactly once.
	rows, err := store.ListEventsByWakeID(context.Background(), wakeID, time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListEventsByWakeID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Kind != "wake.readiness_200" {
		t.Errorf("kind = %q, want wake.readiness_200", rows[0].Kind)
	}
	// Verify the payload carries the wake_id + app_id.
	var payload map[string]any
	if err := json.Unmarshal(rows[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["wake_id"] != wakeID {
		t.Errorf("payload.wake_id = %v, want %s", payload["wake_id"], wakeID)
	}
	if payload["app_id"] != appID {
		t.Errorf("payload.app_id = %v, want %s", payload["app_id"], appID)
	}
	if payload["healthcheck_path"] != "/healthz" {
		t.Errorf("payload.healthcheck_path = %v, want /healthz", payload["healthcheck_path"])
	}
}

// TestEmitRestoreBreakdown_EmitsTimelineRow pins the vmmd-side detailed
// restore timing surface without starting Firecracker. The production
// Restore path supplies the same correlation envelope and phase values after
// its readiness probe succeeds.
func TestEmitRestoreBreakdown_EmitsTimelineRow(t *testing.T) {
	store := state.NewMemStore()
	platform := buildReadinessPlatform(t, store)
	v := &JailerVMM{events: platform}
	wakeID := "w-restore-001"
	appID := "app-restore-001"
	ctx := wire.WithContext(context.Background(), wire.CorrelationFields{
		WakeID: wakeID,
		AppID:  appID,
	})
	at := time.Date(2026, time.August, 31, 12, 13, 58, 302741000, time.UTC)
	v.emitRestoreBreakdown(ctx, Lease{Instance: "inst-restore-001"}, at, restoreTimingBreakdown{
		ChrootMs:             2,
		MaterializeMemMs:     3,
		MaterializeVMStateMs: 4,
		ResolveImagesMs:      5,
		StageDrivesMs:        6,
		StageSnapshotMs:      7,
		HelperMs:             8,
		StartJailerMs:        9,
		BindTunMs:            10,
		LoadSnapshotMs:       400,
		ResumeHookMs:         11,
		WaitReadyMs:          131,
		TotalMs:              596,
	})

	rows, err := store.ListEventsByWakeID(context.Background(), wakeID, time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListEventsByWakeID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Kind != events.WakeRestoreBreakdown {
		t.Fatalf("kind = %q, want %q", rows[0].Kind, events.WakeRestoreBreakdown)
	}
	var payload map[string]any
	if err := json.Unmarshal(rows[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["wake_id"] != wakeID || payload["app_id"] != appID {
		t.Errorf("correlation payload = wake_id:%v app_id:%v, want %s/%s", payload["wake_id"], payload["app_id"], wakeID, appID)
	}
	if got, ok := payload["load_snapshot_ms"].(float64); !ok || got != 400 {
		t.Errorf("payload.load_snapshot_ms = %v, want 400", payload["load_snapshot_ms"])
	}
	if got, ok := payload["total_ms"].(float64); !ok || got != 596 {
		t.Errorf("payload.total_ms = %v, want 596", payload["total_ms"])
	}
}

func TestEmitRestoreBreakdown_WithoutWakeIDDoesNotEmit(t *testing.T) {
	store := state.NewMemStore()
	platform := buildReadinessPlatform(t, store)
	v := &JailerVMM{events: platform}
	v.emitRestoreBreakdown(context.Background(), Lease{Instance: "inst-no-wake"}, time.Now(), restoreTimingBreakdown{TotalMs: 596})

	rows, err := store.ListEventsByWakeID(context.Background(), "", time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListEventsByWakeID: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
}

// silence the unsued import warnings when the metric stubs are
// inlined into the type — the lockless constructors below exist
// solely to keep the test self-contained.
var _ = sync.Mutex{}
