//go:build metal

// guest_tracing_metal_test.go — ADR-958 native acceptance for zero-config
// in-guest tracing. Each scenario deploys an app with `tracing.enabled`,
// wakes it from its init snapshot with one request carrying an unsampled
// traceparent, and requires the app's span on the debugger evidence:
//
//	guest-init env stamping → 127.0.0.1:4318 bridge → vsock 1041 → vmmd
//	broker (host-owned identity) → apid IngestGuestSpans → accumulator flush
//	→ request_telemetry.spans_summary → GET /v1/apps/{slug}/debug/requests/{id}/evidence
//
// TestGuestTracingMetal uses an app with an OTel SDK compiled in and no
// tracing configuration. TestGuestTracingNodePreloadMetal uses a plain Node
// app with no tracing code at all: guest-init's NODE_OPTIONS preload of the
// pinned auto-instrumentation must produce its HTTP client span, which the
// debugger classifies as an app_dependency. TestGuestTracingPythonMetal does
// the same for a plain Flask app through the sitecustomize bootstrap. Both
// managed-runtime apps log one error line (pino, standard logging), which
// must reach the evidence as a redacted runtime log line linked by the
// request's trace (ADR-213 runtime adapter: bridge /v1/logs → vmmd broker →
// apid IngestGuestLogs → log_events).
//
// Build tag: metal. Requires /dev/kvm + root, FAAS_TEST_KERNEL and
// FAAS_BUILDER_BASE_PATH. On hosts without a matching Go toolchain set
// FAAS_E2E_HELLO_SERVER_BINARY, FAAS_E2E_TRACING_SERVER_BINARY and
// FAAS_E2E_GO_TRACING_HELPER_BINARY. The Node and Python scenarios need
// FAAS_E2E_NODE_TRACING_IMAGE_DIR and FAAS_E2E_PYTHON_TRACING_IMAGE_DIR:
// runner-base images with /opt/gregale/tracing, built by
// pkg/e2etest/testdata/tracingfixtures/build.py (see e2etest.PrebuiltImage).

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

type guestTracingScenario struct {
	slug string
	// addImage registers the app image with the fake registry and returns
	// its digest-pinned reference.
	addImage func(*e2etest.FakeRegistry) string
	// respPrefix is the expected start of the woken app's response body.
	respPrefix string
	// evidenceOK reports whether the request evidence shows the app's span.
	evidenceOK func(detail string) bool
}

// adr: 958 — in-guest spans reach the debugger with no app tracing config.
func TestGuestTracingMetal(t *testing.T) {
	runGuestTracingScenario(t, guestTracingScenario{
		slug: "guest-tracing",
		addImage: func(registry *e2etest.FakeRegistry) string {
			image, _ := e2etest.TracingImage("library/guest-tracing")
			return registry.AddImage("library/guest-tracing", image)
		},
		respPrefix: "traced ",
		evidenceOK: func(detail string) bool { return strings.Contains(detail, "SELECT orders") },
	})
}

// adr: 958 — a Go app instrumented only with the SDK's tracing package (no
// OpenTelemetry dependency) reports its database and HTTP calls as
// app_dependency spans on the request evidence.
func TestGuestTracingGoHelperMetal(t *testing.T) {
	runGuestTracingScenario(t, guestTracingScenario{
		slug: "guest-tracing-go",
		addImage: func(registry *e2etest.FakeRegistry) string {
			image, _ := e2etest.GoTracingHelperImage("library/guest-tracing-go")
			return registry.AddImage("library/guest-tracing-go", image)
		},
		// The fixture echoes FAAS_TRACING_ENABLED: guest-init enabled the helper.
		respPrefix: "helper 1",
		evidenceOK: func(detail string) bool {
			return strings.Contains(detail, "SELECT orders") &&
				strings.Contains(detail, `"dependency_kind":"postgresql"`) &&
				strings.Contains(detail, `"dependency_kind":"http"`)
		},
	})
}

// adr: 958 — the Node preload instruments an app that has no tracing code.
func TestGuestTracingNodePreloadMetal(t *testing.T) {
	dir := os.Getenv("FAAS_E2E_NODE_TRACING_IMAGE_DIR")
	if dir == "" {
		t.Skip("FAAS_E2E_NODE_TRACING_IMAGE_DIR unset; skipping Node preload acceptance")
	}
	runGuestTracingScenario(t, guestTracingScenario{
		slug: "guest-tracing-node",
		addImage: func(registry *e2etest.FakeRegistry) string {
			image, _, err := e2etest.PrebuiltImage("library/guest-tracing-node", dir)
			if err != nil {
				t.Fatalf("prebuilt Node image: %v", err)
			}
			return registry.AddImage("library/guest-tracing-node", image)
		},
		// The fixture echoes FAAS_TRACING_ENABLED, proving guest-init stamped
		// the preload environment for this process.
		respPrefix: "preloaded 1",
		evidenceOK: func(detail string) bool {
			return strings.Contains(detail, `"dependency_type":"app_dependency"`) &&
				strings.Contains(detail, `"dependency_kind":"http"`) && hasRedactedErrorLog(detail)
		},
	})
}

// adr: 958 — the Python bootstrap instruments a Flask app with no tracing
// code. adr: 213 — its logging records reach the request evidence.
func TestGuestTracingPythonMetal(t *testing.T) {
	dir := os.Getenv("FAAS_E2E_PYTHON_TRACING_IMAGE_DIR")
	if dir == "" {
		t.Skip("FAAS_E2E_PYTHON_TRACING_IMAGE_DIR unset; skipping Python bootstrap acceptance")
	}
	runGuestTracingScenario(t, guestTracingScenario{
		slug: "guest-tracing-python",
		addImage: func(registry *e2etest.FakeRegistry) string {
			image, _, err := e2etest.PrebuiltImage("library/guest-tracing-python", dir)
			if err != nil {
				t.Fatalf("prebuilt Python image: %v", err)
			}
			return registry.AddImage("library/guest-tracing-python", image)
		},
		respPrefix: "python 1",
		evidenceOK: func(detail string) bool {
			return strings.Contains(detail, `"name":"GET /checkout"`) && hasRedactedErrorLog(detail)
		},
	})
}

// hasRedactedErrorLog reports whether request evidence carries the fixtures'
// error line as a runtime log line, with the customer email redacted.
func hasRedactedErrorLog(detail string) bool {
	var evidence api.DebugRequestEvidenceResponse
	if json.Unmarshal([]byte(detail), &evidence) != nil || strings.Contains(detail, "buyer@example.com") {
		return false
	}
	for _, line := range evidence.Logs {
		if line.Source == string(state.LogEventSourceRuntime) && line.Level == "error" &&
			line.Message == "charge failed for [REDACTED:email]: card declined" {
			return true
		}
	}
	return false
}

func runGuestTracingScenario(t *testing.T, sc guestTracingScenario) {
	t.Helper()
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
		// Flush spans after the gateway's 5s request-row publisher; a span
		// that still beats its row is retried (no_row) on the next flush.
		"FAAS_OTEL_FLUSH_INTERVAL=10s",
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", sc.slug)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake, env...)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanPro)

	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: sc.slug, Type: "app", RequireAuthn: &falsy,
		Tracing: &api.TracingConfig{Enabled: true},
	}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, sc.slug)

	ref := sc.addImage(registry)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+sc.slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID := parseImageDeployment(t, body)
	// Fixtures are full-rootfs images; imaging plus the layer scan takes well
	// over a minute on a small acceptance host.
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
		headers, respBody, respStatus = doReqHeaders(t, h, sc.slug+".apps.test.example", http.MethodGet, "/checkout", nil,
			map[string]string{"Authorization": "Bearer " + key, "Traceparent": "00-" + traceID + "-00f067aa0ba902b7-00"})
		if respStatus == http.StatusOK && strings.HasPrefix(string(respBody), sc.respPrefix) {
			break
		}
		if time.Now().After(wakeDeadline) {
			t.Fatalf("traced request: status=%d body=%q", respStatus, respBody)
		}
		time.Sleep(2 * time.Second)
	}
	gotTrace := headers.Get(api.TraceIDHeader)
	if gotTrace == "" {
		gotTrace = traceID
	}
	// The request woke a live instance. Park it before the harness stops so
	// no Firecracker process outlives the test (cleanups run in LIFO order,
	// ahead of the harness teardown registered by Start).
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cleanupCancel()
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+sc.slug+"/park", nil); status != http.StatusNoContent {
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
		listBody, listStatus := doReq(t, h, key, http.MethodGet, "/v1/apps/"+sc.slug+"/debug/requests", nil)
		if listStatus == http.StatusOK {
			var list api.DebugTelemetryListResponse
			if err := json.Unmarshal(listBody, &list); err != nil {
				t.Fatalf("decode request list: %v body=%s", err, listBody)
			}
			for _, item := range list.Requests {
				if item.TraceID == nil || *item.TraceID != gotTrace {
					continue
				}
				detail, detailStatus := doReq(t, h, key, http.MethodGet, "/v1/apps/"+sc.slug+"/debug/requests/"+item.ID+"/evidence", nil)
				lastDetail = string(detail)
				if detailStatus == http.StatusOK && sc.evidenceOK(lastDetail) {
					return
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("debugger request for trace %s never showed the guest span; last detail: %s", gotTrace, lastDetail)
}
