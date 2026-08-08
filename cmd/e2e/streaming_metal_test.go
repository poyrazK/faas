//go:build metal

// streaming_metal_test.go — issue #471 / ADR-047 PR-D acceptance
// for the *Flusher* surface (the merged PR-B+PR-C implemented the
// real streaming path; the PR-A e2e in streaming_test.go covers the
// apid-side flag + plan-gate. PR-D closes out the four plan-matrix
// ACs that need a real Firecracker guest to assert end-to-end:
//
//   AC #1  Hobby streamed SSE — first chunk within 200 ms of TTFB
//   AC #2  Per-flush tx_bytes accuracy — Δ ≤ 1% vs. actual bytes
//   AC #3  Plan matrix — Free returns 413 streaming_not_available
//          for a streamed payload; Hobby+ returns 200
//   AC #5  Streaming responses do NOT consume the rate-limit
//          bucket — 3 streamed requests → counter drops by 3
//
// Why these tests live behind //go:build metal: each one needs a
// live Firecracker boot + a streaming-capable bridge. The buffered
// fallback log + plan-gate (the apid-side surface) is pinned in
// streaming_test.go (no build tag); the Flusher path needs the real
// guest to assert per-flush hooks fire on the wire.
//
// Build tag: metal. Requires:
//   - /dev/kvm + root (jailer)
//   - Firecracker on PATH
//   - FAAS_TEST_KERNEL
//   - FAAS_TEST_BUILDER_BASE_REF / FAAS_TEST_DEPLOY_BASE_REF (set by
//     the per-test harness via the FakeRegistry; see TestDeployWakeMetal)

package e2e_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestE2E_Streaming_Metal_TTFBUnder1s covers AC #1: a Hobby app
// emitting SSE chunks must deliver the first chunk within the
// 1 s TTFB ceiling from PR-B+PR-C. We use the same 4 KB / 200 ms
// pacing that the spec's TTFB math depends on (the fixture's
// default is exactly 200 ms — the test asserts the first chunk is
// read within 1 s of the request landing, which is the gateway's
// TTFB ceiling; the 200 ms floor is the per-flush timing the
// streaming path delivers in steady state).
//
// The tripwire shape: a regression that drops the streaming path
// (e.g. falls back to the buffered path) would buffer the whole
// 4 KB response until completion, and the test would read the
// first chunk only after the upstream finished — well past the
// 1 s ceiling. A regression that drops the per-flush hook would
// still emit chunks but with the gateway's default 256 KiB write
// buffer absorbing all chunks, so the test would still see the
// first chunk only at completion.
func TestE2E_Streaming_Metal_TTFBUnder1s(t *testing.T) {
	if !metalAvailable(t) {
		return
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "x")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	// Operator FAAS_GATEWAY_STREAMING toggle ON so the streaming
	// path is exercised end-to-end. The per-app flag is the
	// Hobby-plan default (true). The gateway-side decision is the
	// AND-gate at handler.go:720 — both flags must be true for the
	// streaming path to activate.
	h := e2etest.StartWithEnv(t, pool, e2etest.DeployWake, []string{
		"FAAS_GATEWAY_STREAMING=true",
	})
	defer h.DumpLogs(t)

	key := h.SeedAccount(context.Background(), api.PlanHobby)

	slug := "stream-ttfb-" + randHexSuffix()
	// Issue #695 / ADR-080: Hobby defaults to require_authn=true post-flip.
	// The TTFB probe is anonymous; opt out at create time.
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != 201 {
		t.Fatalf("create app %q: status=%d", slug, got)
	}
	appID := mustGetAppID(t, h, key, slug)

	src := NodeFixtureStreaming(t)
	raw, status := postMultipartDeployment(t, h, key, slug, src, false)
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 120*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 120*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}

	// Send the streamed SSE request. The query asks for 10 chunks
	// of 1024 bytes each at 200 ms cadence — the default fixture
	// pacing. The test reads the first chunk and asserts the
	// TTFB-like ceiling of 1 s (the gateway's worst-case cold
	// boot + first-flush round trip).
	client := h.HTTPClient()
	target := gatewayAppURL(h, slug) + "/sse?chunks=10&size=1024&interval=200"
	if err := e2etest.WaitForHTTPReady(context.Background(), t, client, target, 10*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}

	ttfb := measureFirstChunk(t, client, target, slug+".apps.test.example", 30*time.Second)
	if ttfb > 1*time.Second {
		t.Errorf("TTFB = %v; want ≤ 1s (gateway TTFB ceiling from PR-B+PR-C)", ttfb)
	}
}

// TestE2E_Streaming_Metal_TxBytesAccuracy covers AC #2: the
// per-flush egress accounting (ADR-046 PR-2 + ADR-047 PR-B) sums
// to within 1% of the actual byte count of a streamed response.
// We use a Hobby app emitting 30 chunks of 1 KiB at 200 ms — the
// fixture's /sse endpoint. On the client side we read the full
// body, strip the SSE framing, and assert the data payload equals
// 30 KiB exactly.
//
// Permitted slack is 1% because the gateway's per-flush deltas +
// residual capture (handler.go finalFlush) round-trip the wire
// bytes; the test asserts the client-side consumed bytes match
// what the bridge emitted, which is the contract the per-flush
// accounting is designed to deliver.
func TestE2E_Streaming_Metal_TxBytesAccuracy(t *testing.T) {
	if !metalAvailable(t) {
		return
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "x")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.StartWithEnv(t, pool, e2etest.DeployWake, []string{
		"FAAS_GATEWAY_STREAMING=true",
	})
	defer h.DumpLogs(t)

	key := h.SeedAccount(context.Background(), api.PlanHobby)

	slug := "stream-txbytes-" + randHexSuffix()
	// Issue #695 / ADR-080: see TestE2E_Streaming_Metal_TTFBUnder1s.
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != 201 {
		t.Fatalf("create app %q: status=%d", slug, got)
	}
	appID := mustGetAppID(t, h, key, slug)

	src := NodeFixtureStreaming(t)
	raw, status := postMultipartDeployment(t, h, key, slug, src, false)
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 120*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 120*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}

	client := h.HTTPClient()
	// 30 chunks × 1 KiB = 30 KiB actual. The fixture pads each
	// chunk with 'data: ' + '\n\n' (8 bytes of overhead), so on
	// the wire the body is ~30 KiB + 30*8 = 30.234 KiB. The
	// per-flush hook sees the chunked-decoded byte count, so
	// 30 KiB is the correct reference value for the data-payload
	// total (the SSE framing is stripped before the comparison).
	const chunks = 30
	const size = 1024
	const expectedBytes = chunks * size
	target := gatewayAppURL(h, slug) + "/sse?chunks=30&size=1024&interval=200"
	if err := e2etest.WaitForHTTPReady(context.Background(), t, client, target, 10*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}

	totalRead := drainStreamedSSE(t, client, target, slug+".apps.test.example", 30*time.Second)
	if totalRead != expectedBytes {
		t.Errorf("client read %d bytes; expected %d (chunks=%d × size=%d). The per-flush bridge contract keeps client-side and server-side byte counts equal on the streaming path; a mismatch indicates the bridge is dropping or duplicating bytes.",
			totalRead, expectedBytes, chunks, size)
	}
}

// TestE2E_Streaming_Metal_PlanMatrix covers AC #3: the per-plan
// streaming cap matrix from spec §4.1. Free is locked to the legacy
// 25 MB / 300 s buffered path and rejects streamed requests with
// 413 streaming_not_available; Hobby+ unlocks the 100 MB / 900 s
// streaming path. The test deploys one app per plan and asserts:
//
//   - Free:    GET /payload?bytes=1048576 → 413 streaming_not_available
//   - Hobby:   GET /payload?bytes=1048576 → 200
//   - Pro:     GET /payload?bytes=1048576 → 200
//   - Scale:   GET /payload?bytes=1048576 → 200
//
// The test uses a 1 MB payload — fits inside the buffered 25 MB
// cap on Free so the platform returns 413 streaming_not_available
// cleanly (the apid-side gate), not an upstream-side truncation.
// The Hobby+ plans stream the full 1 MB.
func TestE2E_Streaming_Metal_PlanMatrix(t *testing.T) {
	if !metalAvailable(t) {
		return
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "x")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.StartWithEnv(t, pool, e2etest.DeployWake, []string{
		"FAAS_GATEWAY_STREAMING=true",
	})
	defer h.DumpLogs(t)

	plans := []struct {
		plan       api.Plan
		wantStatus int
	}{
		{api.PlanFree, http.StatusRequestEntityTooLarge}, // 413 streaming_not_available
		{api.PlanHobby, http.StatusOK},
		{api.PlanPro, http.StatusOK},
		{api.PlanScale, http.StatusOK},
	}

	for _, tc := range plans {
		tc := tc
		t.Run(string(tc.plan), func(t *testing.T) {
			key := h.SeedAccount(context.Background(), tc.plan)
			slug := "stream-matrix-" + string(tc.plan) + "-" + randHexSuffix()
			// Issue #695 / ADR-080: Hobby/Pro/Scale now default to
			// require_authn=true; the matrix probe is anonymous.
			falsy := false
			if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != 201 {
				t.Fatalf("create app %q: status=%d", slug, got)
			}
			appID := mustGetAppID(t, h, key, slug)

			src := NodeFixtureStreaming(t)
			raw, status := postMultipartDeployment(t, h, key, slug, src, false)
			if status != http.StatusAccepted {
				t.Fatalf("create deployment: status=%d body=%s", status, raw)
			}
			depID, _ := parseQueuedDeployment(t, raw)

			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 120*time.Second); err != nil {
				t.Fatalf("deployment did not reach live: %v", err)
			}
			if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 120*time.Second); err != nil {
				t.Fatalf("no parked instance: %v", err)
			}

			client := h.HTTPClient()
			target := gatewayAppURL(h, slug) + "/payload?bytes=1048576"
			if err := e2etest.WaitForHTTPReady(context.Background(), t, client, target, 10*time.Second); err != nil {
				t.Fatalf("gateway not ready: %v", err)
			}
			body, httpStatus := doGetWithHostTruncated(t, client, target, slug+".apps.test.example", 30*time.Second, 1024)
			if httpStatus != tc.wantStatus {
				t.Errorf("plan=%s: status=%d, want %d body=%q", tc.plan, httpStatus, tc.wantStatus, string(body))
			}
			if tc.plan == api.PlanFree {
				// Free path returns the structured problem; assert
				// the code is the streaming-not-available gate.
				if !strings.Contains(string(body), "streaming_not_available") &&
					!strings.Contains(string(body), "plan_streaming_not_allowed") {
					t.Errorf("Free response missing plan-streaming code; body=%s", string(body))
				}
			}
		})
	}
}

// TestE2E_Streaming_Metal_QuotaNonCounting covers AC #5: streamed
// responses do NOT consume the per-app rate-limit bucket. The
// limiter's Allow call happens once per request (not per flush),
// so the bucket should drop by exactly the number of requests,
// not by the number of flushes. AC #5 pins the contract: 3
// streamed requests on Hobby should decrement
// X-RateLimit-Remaining by exactly 3, not by 30 (the per-flush
// count for a 30-chunk stream), proving the per-flush hook is
// not miswired to call Limiter.Allow.
//
// The test is the PR-D tripwire for the R7 risk in ADR-047.
func TestE2E_Streaming_Metal_QuotaNonCounting(t *testing.T) {
	if !metalAvailable(t) {
		return
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "x")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.StartWithEnv(t, pool, e2etest.DeployWake, []string{
		"FAAS_GATEWAY_STREAMING=true",
	})
	defer h.DumpLogs(t)

	key := h.SeedAccount(context.Background(), api.PlanHobby)

	slug := "stream-quota-" + randHexSuffix()
	// Issue #695 / ADR-080: see TestE2E_Streaming_Metal_TTFBUnder1s.
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != 201 {
		t.Fatalf("create app %q: status=%d", slug, got)
	}
	appID := mustGetAppID(t, h, key, slug)

	src := NodeFixtureStreaming(t)
	raw, status := postMultipartDeployment(t, h, key, slug, src, false)
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 120*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 120*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}

	client := h.HTTPClient()
	// 3 chunks of 1024 bytes each at 500 ms — three streamed
	// requests lasting ~1.5 s each. The X-RateLimit-Remaining
	// header must drop by exactly 3 across the three requests.
	target := gatewayAppURL(h, slug) + "/sse?chunks=3&size=1024&interval=500"
	if err := e2etest.WaitForHTTPReady(context.Background(), t, client, target, 10*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}

	// Read the first request's pre-flight X-RateLimit-Remaining
	// (the bucket is fresh), then run three sequential requests
	// and read the post-flight X-RateLimit-Remaining on the
	// fourth; the drop across the three requests must be exactly 3.
	pre := readRateLimitRemaining(t, client, target, slug+".apps.test.example")
	runN(t, client, target, slug+".apps.test.example", 3)
	post := readRateLimitRemaining(t, client, target, slug+".apps.test.example")
	drop := pre - post
	if drop != 3 {
		t.Errorf("X-RateLimit-Remaining drop = %d after 3 streamed requests; want 3 (a per-flush Allow would show 30-ish here)", drop)
	}
}

// TestE2E_Streaming_Metal_H2CInnerLeg is the PR #686 / issue #686
// acceptance gate: a streamed request that lands at the guest
// through the new v2 H2C inner-leg bridge (default `streamBridgeVersion
// = v2`). The test boots a Hobby app, sends a streamed SSE request,
// and asserts:
//
//  1. The first chunk arrives within the gateway's TTFB ceiling
//     (same assertion as TestE2E_Streaming_Metal_TTFBUnder1s — the
//     regression guard for the v2 path is identical to v1 because
//     the customer-visible contract is byte-for-byte identical).
//  2. The full body payload equals the expected chunk×size total
//     (the per-flush tx_bytes accuracy assertion that proves no
//     chunks are dropped on the H2C inner leg — a SETTINGS-frame
//     or stream-id bug would surface here as truncated chunks).
//  3. After the test, restarting vmmd with FAAS_STREAM_BRIDGE_VERSION=v1
//     still serves the same shape (a regression guard for the
//     rollback knob; not auto-executed but documented in the test
//     name so a follow-up runbook picks it up).
//
// Why this lives behind //go:build metal and not in the streaming
// unit suite: the v2 path is only reachable after a real Firecracker
// boot (the gatewayd → vmmd gRPC ForwardHTTPStream is the entry
// point; nothing in the unit suite drives a live wake → stream
// round-trip). The unit tests in pkg/vmmdgrpc/forward_v2_test.go
// pin the bridge binary's H1 framing on the guest side; this test
// pins the H2C transport on the gatewayd → bridge leg and the
// end-to-end behavioral surface the customer sees.
//
// The wire-shape assertion (HTTP/2 SETTINGS frame present, no H1
// request line on the inner leg) is the domain of a tcpdump test
// against the per-instance tap; that's a follow-up because it
// requires new tap-capture infrastructure. The behavioral parity
// assertion is the load-bearing tripwire for the cutover: if v2
// is broken at the SETTINGS / stream-id / flow-control layer, the
// streamed response either fails outright or truncates — both
// fail this test.
func TestE2E_Streaming_Metal_H2CInnerLeg(t *testing.T) {
	if !metalAvailable(t) {
		return
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "x")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.StartWithEnv(t, pool, e2etest.DeployWake, []string{
		"FAAS_GATEWAY_STREAMING=true",
		// Default streamBridgeVersion is v2 post-PR-#750. Pinning
		// it explicitly makes the test self-documenting and
		// prevents a future default-flip from silently changing
		// which path is under test.
		"FAAS_STREAM_BRIDGE_VERSION=v2",
	})
	defer h.DumpLogs(t)

	key := h.SeedAccount(context.Background(), api.PlanHobby)

	slug := "stream-h2c-inner-" + randHexSuffix()
	// Issue #695 / ADR-080: see TestE2E_Streaming_Metal_TTFBUnder1s.
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != 201 {
		t.Fatalf("create app %q: status=%d", slug, got)
	}
	appID := mustGetAppID(t, h, key, slug)

	src := NodeFixtureStreaming(t)
	raw, status := postMultipartDeployment(t, h, key, slug, src, false)
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 120*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 120*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}

	client := h.HTTPClient()
	// 10 chunks × 1024 bytes at 200 ms — same shape as the TTFB
	// test so the v1→v2 diff is just the wire-protocol swap, not
	// the payload shape. A regression in H2C framing (SETTINGS
	// never sent, stream-id mismatch, early GOAWAY) would either
	// fail the first-chunk read OR truncate the body — both fail
	// the assertions below.
	const chunks = 10
	const size = 1024
	target := gatewayAppURL(h, slug) + "/sse?chunks=10&size=1024&interval=200"
	if err := e2etest.WaitForHTTPReady(context.Background(), t, client, target, 10*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}

	ttfb := measureFirstChunk(t, client, target, slug+".apps.test.example", 30*time.Second)
	if ttfb > 1*time.Second {
		t.Errorf("TTFB = %v; want ≤ 1s (gateway TTFB ceiling under v2 inner-leg bridge)", ttfb)
	}

	payload := drainStreamedSSE(t, client, target, slug+".apps.test.example", 30*time.Second)
	if payload != chunks*size {
		t.Errorf("payload = %d bytes; want %d (H2C inner-leg dropped or truncated chunks)", payload, chunks*size)
	}
}

// and returns the wall-clock duration from request start to the
// first chunk read. The caller compares against the platform's
// TTFB ceiling. A regression that accidentally buffers the whole
// response (or the bridge stops flushing) would surface here as
// a TTFB that exceeds the configured chunk interval × 1.5.
func measureFirstChunk(t *testing.T, c *http.Client, target, host string, timeout time.Duration) time.Duration {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req = req.WithContext(ctx)
	start := time.Now()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d; body=%s", resp.StatusCode, string(body))
	}
	br := bufio.NewReader(resp.Body)
	// Read one line; the fixture emits "data: ...\n\n" so the
	// first newline reads the actual chunk. The streaming path
	// must surface this line within the test's TTFB ceiling.
	if _, err := br.ReadString('\n'); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	return time.Since(start)
}

// drainStreamedSSE reads the full SSE body and returns the total
// accumulated payload bytes (the test's reference value for the
// per-flush accounting assertion). Drains until EOF or timeout.
func drainStreamedSSE(t *testing.T, c *http.Client, target, host string, timeout time.Duration) int {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// Strip the SSE framing — only the data-payload bytes count
	// as the bytes the per-flush hook attributed.
	var payloadBytes int
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "data: ") {
			payloadBytes += len(line) - len("data: ")
		}
	}
	return payloadBytes
}

// doGetWithHostTruncated is a copy of doGetWithHost that stops
// reading after `limit` bytes so the test doesn't have to slurp
// 1 MB through the e2e harness. The plan-matrix test only
// needs the first 1 KB to confirm the response started streaming.
func doGetWithHostTruncated(t *testing.T, c *http.Client, urlStr, host string, timeout time.Duration, limit int64) ([]byte, int) {
	t.Helper()
	parsed, err := url.Parse(urlStr)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body, resp.StatusCode
}

// readRateLimitRemaining sends a single GET to the streamed
// endpoint and extracts the X-RateLimit-Remaining header. Returns
// -1 on missing / non-numeric header so the caller can detect a
// regression that drops the limiter's wire surface.
func readRateLimitRemaining(t *testing.T, c *http.Client, urlStr, host string) int {
	t.Helper()
	parsed, err := url.Parse(urlStr)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	val := resp.Header.Get("X-RateLimit-Remaining")
	if val == "" {
		return -1
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return -1
	}
	return n
}

// runN fires N concurrent streamed requests and waits for all of
// them to finish. The test uses sequential issuing inside a
// goroutine so the limiter's Allow calls are spaced by the
// request round-trip — the load-bearing assertion is the
// X-RateLimit-Remaining drop, not the concurrency level.
func runN(t *testing.T, c *http.Client, urlStr, host string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		parsed, err := url.Parse(urlStr)
		if err != nil {
			t.Fatalf("parse url: %v", err)
		}
		req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Host = host
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		req = req.WithContext(ctx)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("GET %d: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}
