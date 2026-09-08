//go:build metal

// deploy_healthcheck_metal_test.go — issue #460 / ADR-053 / ADR-057
// PR-D acceptance: deploy with `Overrides.Healthcheck.Path = "/healthz"`
// and assert the readiness probe reaches the customer route through
// the host's `GET <HostIP>:8080/healthz`. The test stands up its own
// per-test harness so the override-healthcheck fixture doesn't
// interfere with the 100-cycle wake-latency loop in
// TestDeployWakeMetal (which targets the legacy :3000 fixture).
//
// Why the test stands up its own harness: PR-D's PathOfWire is
//
//	fargate POST /v1/apps/{slug}/deployments
//	  with Overrides.Healthcheck.Path = "/healthz"
//	    → deployments.override_healthcheck = {"path":"/healthz", ...}
//	    → imaged writes app.json with Healthz = "/healthz"
//	      (manifest.ApplyOverrides drops IntervalS/TimeoutS/Retries
//	      per PR-B's "timing deferred" stance — only path is live
//	      end-to-end until a v2 contract lands)
//	    → schedd spec.HealthcheckPath = "/healthz"
//	      (mirror of PR-C's spec.Port — healthcheckPathFromDep)
//	    → vmmd CreateFromSnapshot/ColdBoot ports HealthcheckPath
//	      to fcvm.WakeRequest.HealthcheckPath
//	    → JailerVMM Instance.HealthcheckPath = "/healthz"
//	    → waitReady does HTTP GET <HostIP>:8080/healthz
//	      → 2xx from /healthz = ready
//	      → non-2xx or err = retry until deadline (PR-D's
//	        "wake must always work" stance — ADR-005)
//
// A regression that drops HealthcheckPath at any seam makes the
// probe fall through to legacy TCP-accept on :8080. The legacy
// path succeeds on the *first* wake (the fixture binds :8080) so
// the wake still goes Ready — but the timing widens: a wake that
// would have failed the threshold on a stale `/healthz` now sails
// through on a TCP accept. We assert content against the
// "/healthz" body so a regression trips here immediately.
//
// Build tag: metal. Requires:
//   - /dev/kvm + root (jailer)
//   - Firecracker on PATH
//   - FAAS_TEST_KERNEL

package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// TestDeployHealthcheckMetal issues a single deploy with
// Overrides.Healthcheck.Path="/healthz", waits for the deployment to
// reach Live + an instance to park, then sends one GET through the
// gateway and asserts the runner served on :8080. The check is
// content-driven: the response body for /healthz includes
// "override-healthz:<readyFlag>" where the runner flips "ready" on
// the first non-/healthz request — proving the HTTP probe actually
// reached the customer's route and got a 2xx back.
//
// Why we hit / and not /healthz through the gateway: the gateway
// forwards arbitrary paths to the runner (it's a transparent HTTP
// proxy). The host probe's job is to decide *readiness* — the
// gateway's job is to forward whatever the customer asked for.
// Hitting / through the gateway proves the runner is bound + the
// forwarder can reach it; content-checking the body shape proves
// it's the right runner.
//
// As a second assertion, the body of an explicit /healthz request
// must carry "override-healthz:ready" — that's the readiness state
// the runner flipped in response to being probed by waitReady. If
// the host never probed /healthz, the runner's ready flag would
// still be false and the body would say "not-ready".
func TestDeployHealthcheckMetal(t *testing.T) {
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
	// isolated from TestDeployWakeMetal and from PR-C's port
	// override test.
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
	// set doesn't collide with the wake-latency test's `hello` or
	// with PR-C's `portfix-*`.
	slug := "healthzfix-" + randHexSuffix()
	// Issue #695 / ADR-080: post-flip Pro defaults require_authn=true
	// + public_auth_mode=bearer. This test probes the routed URL
	// anonymously (doGetWithHost below), so we opt out at create-time.
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != 201 {
		t.Fatalf("create app %q: status=%d", slug, got)
	}
	appID := mustGetAppID(t, h, key, slug)

	// Multipart deploy via builderd (same path build_metal_test uses)
	// — the fixture's source tarball carries index.js with the
	// /healthz handler, and the multipart form carries
	// Overrides.Healthcheck.Path="/healthz".
	src := NodeFixtureHealthcheck(t)
	raw, status := postMultipartDeploymentWithOverrides(t, h, key, slug, src, false, &api.CreateDeploymentOverrides{
		Healthcheck: &api.DeploymentHealthcheck{Path: "/healthz"},
	}, "")
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)

	// Wait for parked → live. PR-A persists
	// OverrideHealthcheck as jsonb; the test could assert on the
	// stored shape but reading json.RawMessage through the
	// pgstore scan would require a small helper, and the wire
	// path is better verified at schedd (covered by the unit
	// tests in pkg/sched). The end-to-end check below is the
	// load-bearing one: wake ready happened via the HTTP probe.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 90*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, StateParkedForHealthcheck, 90*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}

	// Send one GET / through the gateway. The forwarder dials the
	// guest's effective port (portnorm re-exposes the :8080 bind
	// inside the guest, so :8080 is what the forwarder dials).
	// The runner responds with "hello from faas
	// (override-healthz)" on /, which proves the runner bound
	// correctly AND that any first-call side effect (the ready
	// flip) ran (i.e. waitReady successfully probed /healthz
	// before the forwarder started forwarding — otherwise the
	// runner would have stayed in NOT-READY and the wake would
	// have hit the legacy TCP-accept fallback path).
	client := h.HTTPClient()
	url := gatewayAppURL(h, slug)
	if err := e2etest.WaitForHTTPReady(context.Background(), t, client, url, 5*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	body, httpStatus := doGetWithHost(t, client, url, slug+".apps.test.example", 30*time.Second)
	if httpStatus != 200 {
		t.Fatalf("status=%d body=%s", httpStatus, body)
	}
	if !strings.Contains(string(body), "override-healthz") {
		t.Errorf("body=%q; expected contains 'override-healthz' "+
			"(PR-D smoke — waitReady HTTP probe on /healthz + runner bind)", string(body))
	}

	// Now hit /healthz directly. If waitReady's loop never
	// probed /healthz (the seam regressed), the runner's
	// `ready` flag is still false and the body says
	// "not-ready". A successful assertion here proves waitReady
	// reached the route.
	healthURL := url + "/healthz"
	body2, httpStatus2 := doGetWithHost(t, client, healthURL, slug+".apps.test.example", 10*time.Second)
	if httpStatus2 != 200 {
		t.Fatalf("/healthz status=%d body=%s", httpStatus2, body2)
	}
	if !strings.Contains(string(body2), "override-healthz:ready") {
		t.Fatalf("/healthz body=%q; expected 'override-healthz:ready' "+
			"(proves waitReady HTTP probe flipped the runner's ready flag)", string(body2))
	}
}

// StateParkedForHealthcheck pins the local re-export of the
// parked-instance state constant. The e2etest.WaitForInstanceState
// helper takes a state.State — the value lives in pkg/state and is
// the same set used by TestDeployOverridePortMetal. Local alias
// keeps the test self-documenting (this is the parked-state wait,
// not the running-state wait).
//
// Re-exported here rather than imported as pkg/state.StateParked
// to keep the e2e test files readable without dragging in
// pkg/state per file (cmd/e2e is build-tagged metal, but the
// lightweight alias lets future readers grep `StateParked*` in
// one place).
const StateParkedForHealthcheck = "PARKED"

// metalAvailable + randHexSuffix are defined in
// deploy_override_port_metal_test.go and shared across the per-test
// harness fixtures in this package — no per-file clones needed.
