//go:build metal

// deploy_override_port_metal_test.go — issue #460 / ADR-053 PR-C
// acceptance: deploy with `Overrides.Port=9090`, wake, and assert
// the gateway response is served from the override port. The test
// stands up its own harness + fake registry so the override-port
// fixture doesn't interfere with the 100-cycle wake-latency loop
// in TestDeployWakeMetal (which targets the legacy :3000 fixture).
//
// Why the test stands up its own harness: PR-C's PathOfWire is
//
//	fargate POST /v1/apps/{slug}/deployments
//	  with Overrides.Port=9090
//	    → deployments.override_port = 9090
//	    → imaged writes app.json with Port = 9090
//	    → schedd's spec.Port = 9090
//	    → vmmd CreateFromSnapshot/ColdBoot ports Port to
//	      fcvm.WakeRequest.Port
//	    → guest-init runAppWithEnv stamps PORT=9090
//	    → runner binds :9090
//	    → vmmd buildBridgeScript dials 10.0.0.2:9090 (Port=9090)
//	    → gateway Target.Port = 9090 → ForwardHTTPRequestInit.Port = 9090
//
// A regression that drops Port at any seam trips the final
// assertion: the gateway 200's the request only when the vmmd
// bridge successfully dials :9090 (matching what the runner bound).
//
// Build tag: metal. Requires:
//   - /dev/kvm + root (jailer)
//   - Firecracker on PATH
//   - FAAS_TEST_KERNEL

package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDeployOverridePortMetal issues a single deploy with
// Overrides.Port=9090, waits for the deployment to reach Live and
// an instance to park, then sends one GET through the gateway and
// asserts the runner served the request on :9090. The check is
// content-driven: the response body includes "override-port:9090",
// proving the platform contract ran end-to-end.
//
// The fixture (NodeFixturePort) reads `PORT` env var — guest-init
// stamps it from m.EffectivePort(); the vmmd bridge then dials
// 10.0.0.2:9090. If any seam drops Port, the bridge either dials
// :8080 (silently 503s on a guest bound on :9090) or 500s when the
// guest's portnorm ladder fails to find anything on :8080. Both
// surface here.
func TestDeployOverridePortMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Per-test fake registry + per-test harness keep this subtest
	// isolated from TestDeployWakeMetal and from any sibling.
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "x")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	defer h.DumpLogs(t)

	key := h.SeedAccount(context.Background(), api.PlanPro)

	// Per-test app — distinct slug so the harness's per-app target
	// set doesn't collide with the wake-latency test's `hello`.
	// Pick a non-deterministic slug so a previous run's parked
	// instance (if cleanup lost the harness) doesn't shadow the
	// pickup order.
	slug := "portfix-" + randHexSuffix()
	// Issue #695 / ADR-080: post-flip Pro defaults require_authn=true
	// + public_auth_mode=bearer. The doGetWithHost probes below hit
	// the routed URL anonymously — opt out at create-time.
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != 201 {
		t.Fatalf("create app %q: status=%d", slug, got)
	}
	appID := mustGetAppID(t, h, key, slug)

	// Multipart deploy via builderd (same path build_metal_test uses)
	// — the fixture's source tarball carries index.js that reads PORT
	// from env, and the multipart form carries Overrides.Port=9090.
	// The override payload: Port=9090 + a redundant env override
	// (PORT=9090) — the platform contract still wins because
	// guest-init appends PORT AFTER BuildEnvWithSecrets (see
	// guest/init/main_linux.go::runAppWithEnv).
	src := NodeFixturePort(t)
	raw, status := postMultipartDeploymentWithOverrides(t, h, key, slug, src, false, &api.CreateDeploymentOverrides{
		Port: 9090,
		Env:  map[string]string{"PORT": "9090"},
	}, "")
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)

	// Wait for parked → live.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dep, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 90*time.Second)
	if err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if dep.OverridePort != 9090 {
		t.Errorf("OverridePort = %d, want 9090 (PR-A persisted value)", dep.OverridePort)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}

	// Send one GET through the gateway. The host resolves via
	// gatewayd's pgRouter; the picked target's Port must be 9090
	// so the vmmd bridge dials the override port.
	client := h.HTTPClient()
	url := gatewayAppURL(h, slug)
	if err := e2etest.WaitForHTTPReady(context.Background(), t, client, url, 5*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	body2, httpStatus := doGetWithHost(t, client, url, slug+".apps.test.example", 30*time.Second)
	if httpStatus != 200 {
		t.Fatalf("status=%d body=%s", httpStatus, body2)
	}
	// Content check: the fixture's response carries "override-port:<port>"
	// where <port> is what process.env.PORT resolved to inside the
	// guest. If guest-init failed to stamp PORT, the runner would
	// fall back to its hardcoded 8080 and the body would say
	// "override-port:8080" — or the bridge would 503.
	if got := strings.TrimSpace(string(body2)); !strings.Contains(got, "override-port:9090") {
		t.Fatalf("body=%q; expected contains 'override-port:9090' "+
			"(PR-C smoke — guest-init PORT injection + vmmd bridge dial)", got)
	}
}

// metalAvailable pins the same pre-flight checks TestDeployWakeMetal
// uses; both the wake-latency test and this override-port test share
// the conditions because they boot real Firecracker.
func metalAvailable(t *testing.T) bool {
	t.Helper()
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal deploy→wake test")
		return false
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
		return false
	}
	return true
}

// randHexSuffix returns a 16-char random hex string for per-test
// slug collision avoidance. 64 bits makes a collision across
// accumulated failed runs effectively impossible (the previous
// 32-bit version collided at ~1/4B which was non-trivial for a
// test that boots real Firecracker and crashes sometimes leave the
// app's parked instance on disk). `crypto/rand` keeps the helper
// dependency-free.
func randHexSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback is acceptable for a test helper; the slug is
		// cosmetic. We still want a non-empty suffix so the slug
		// matches the fixtures' "portfix-NNNN" pattern.
		return "fallback00000000"
	}
	return hex.EncodeToString(b[:])
}
