//go:build metal

// guest_tracing_metal_test.go — ADR-829 native acceptance for zero-config
// in-guest tracing. A scratch image whose server has an OpenTelemetry SDK
// compiled in, but no tracing configuration of its own, is deployed with
// `tracing.enabled`. One request through the gateway must produce a debugger
// request whose spans include the app's "SELECT orders" client span, proving
// the whole path inside a real Firecracker guest:
//
//	guest-init env stamping → 127.0.0.1:4318 bridge → vsock 1041 → vmmd
//	broker (host-owned identity) → apid IngestGuestSpans → accumulator flush
//	→ request_telemetry.spans_summary → GET /v1/apps/{slug}/debug/requests/{id}/evidence
//
// The inbound traceparent is deliberately unsampled: guest-init's default
// sampler must not let the gateway's head sampling suppress the app's spans.
//
// Build tag: metal. Requires /dev/kvm + root, FAAS_TEST_KERNEL and
// FAAS_BUILDER_BASE_PATH. On hosts without a matching Go toolchain set
// FAAS_E2E_HELLO_SERVER_BINARY and FAAS_E2E_TRACING_SERVER_BINARY.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 829 — in-guest spans reach the debugger with no app tracing config.
func TestGuestTracingMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping guest tracing acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping guest tracing acceptance")
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// One spans-writer socket shared by apid (server) and vmmd (client);
	// extraEnv is appended after the harness defaults, so it wins.
	telemetryDir, err := os.MkdirTemp("", "faas-e2e-tracing-*")
	if err != nil {
		t.Fatalf("telemetry dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(telemetryDir) })
	env := []string{
		"FAAS_GUEST_TRACING_ENABLED=1",
		"FAAS_OTEL_SPANS_WRITER_ENABLED=true",
		"FAAS_APID_OTEL_SPANS_WRITER_SOCKET=" + filepath.Join(telemetryDir, "spans.sock"),
		"FAAS_REQUEST_TELEMETRY_ENABLED=true",
		"FAAS_APID_REQUEST_TELEMETRY_SOCKET=" + filepath.Join(telemetryDir, "request-telemetry.sock"),
		// Flush spans after the gateway's 5s request-row publisher so the
		// UPDATE finds the row.
		"FAAS_OTEL_FLUSH_INTERVAL=10s",
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "guest-tracing")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake, env...)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanPro)

	const slug = "guest-tracing"
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "app", RequireAuthn: &falsy,
		Tracing: &api.TracingConfig{Enabled: true},
	}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)

	image, _ := e2etest.TracingImage("library/guest-tracing")
	ref := registry.AddImage("library/guest-tracing", image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID := parseImageDeployment(t, body)
	// The fixture is a 14 MB full-rootfs image; imaging plus the layer scan
	// takes well over a minute on a small acceptance host.
	deployCtx, deployCancel := context.WithTimeout(context.Background(), 320*time.Second)
	defer deployCancel()
	if _, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, depID, 300*time.Second); err != nil {
		t.Fatalf("tracing deployment did not reach live: %v", err)
	}
	// Image deployments cold-boot once for the init snapshot and park. The
	// traced request then wakes the app from that snapshot, so the spans also
	// prove the bridge and SDK exporter survive restore.
	parkCtx, parkCancel := context.WithTimeout(context.Background(), 125*time.Second)
	defer parkCancel()
	if _, err := e2etest.WaitForInstanceState(parkCtx, t, pool, appID, state.StateParked, 120*time.Second); err != nil {
		t.Fatalf("tracing init snapshot did not park: %v", err)
	}

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	var (
		headers    http.Header
		respBody   []byte
		respStatus int
	)
	wakeDeadline := time.Now().Add(120 * time.Second)
	for {
		headers, respBody, respStatus = doReqHeaders(t, h, slug+".apps.test.example", http.MethodGet, "/checkout", nil,
			map[string]string{"Authorization": "Bearer " + key, "Traceparent": "00-" + traceID + "-00f067aa0ba902b7-00"})
		if respStatus == http.StatusOK && strings.HasPrefix(string(respBody), "traced ") {
			break
		}
		if time.Now().After(wakeDeadline) {
			t.Fatalf("traced request: status=%d body=%q", respStatus, respBody)
		}
		time.Sleep(2 * time.Second)
	}
	gotTrace := headers.Get(api.TraceIDHeader)
	if gotTrace == "" {
		gotTrace = strings.TrimSpace(strings.TrimPrefix(string(respBody), "traced "))
	}
	if gotTrace != traceID {
		t.Logf("gateway continued trace %q (sent %q); matching on the gateway trace", gotTrace, traceID)
	}
	// The request woke a live instance. Park it before the harness stops so
	// no Firecracker process outlives the test (cleanups run in LIFO order,
	// ahead of the harness teardown registered by Start).
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cleanupCancel()
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Errorf("cleanup park: status=%d", status)
			return
		}
		if _, err := e2etest.WaitForInstanceState(cleanupCtx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
			t.Errorf("guest tracing cleanup park: %v", err)
		}
	})

	deadline := time.Now().Add(90 * time.Second)
	var lastDetail string
	for time.Now().Before(deadline) {
		listBody, listStatus := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/debug/requests", nil)
		if listStatus == http.StatusOK {
			var list api.DebugTelemetryListResponse
			if err := json.Unmarshal(listBody, &list); err != nil {
				t.Fatalf("decode request list: %v body=%s", err, listBody)
			}
			for _, item := range list.Requests {
				if item.TraceID == nil || *item.TraceID != gotTrace {
					continue
				}
				detail, detailStatus := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/debug/requests/"+item.ID+"/evidence", nil)
				lastDetail = string(detail)
				if detailStatus == http.StatusOK && strings.Contains(lastDetail, "SELECT orders") {
					return
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("debugger request for trace %s never showed the guest span; last detail: %s", gotTrace, lastDetail)
}
