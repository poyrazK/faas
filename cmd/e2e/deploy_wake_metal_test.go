//go:build metal

// deploy_wake_metal_test.go — M5 §14 acceptance: faas deploy → parked → first
// request wakes; plus the §14 V2 / spec §6.3 wake-latency 100-cycle p50/p95
// loop (closes STATUS.md:201-204 "loop driver doesn't exist").
//
// End-to-end through the real wire: apid → imaged (pull from fake OCI
// registry on loopback) → schedd → vmmd → firecracker, then a real HTTP
// request through gatewayd wakes the parked snapshot and reads back the body
// the registry served.
//
// Build tag: metal. Requires:
//   - /dev/kvm + root (jailer needs CAP_NET_ADMIN, CAP_MKNOD, …)
//   - Firecracker on PATH
//   - FAAS_TEST_KERNEL pointing at a vmlinux (any recent one)
//
// Runs on the dev EX44 via `make test-metal`, or locally on M3+ Mac via
// `make metal-lima` (Lima nested KVM).

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/gateway/testhist"
	"github.com/onebox-faas/faas/pkg/state"
)

// helloBody is the round-trip fixture: the bytes written into app/hello.txt
// (intentionally without a trailing newline — `cat` would echo one if we kept
// it, and the body/trim assertion `strings.TrimSpace(body) == helloBody` is
// comparing the *content* against the trimmed form). Keeping the fixture
// newline-free makes the assertion robust against CRLF or transport-layer
// wrapping on the wire.
const helloBody = "hello from faas"

// TestDeployWakeMetal runs the five-subtest acceptance:
//
//  1. deploy-then-parked              — apid → imaged → schedd → vmmd → parked
//  2. first-request-wakes             — gatewayd request triggers a wake from snapshot
//  3. wake-latency-p50p95-100cycles   — §14 V2 / spec §6.3 SLO gate (p50 ≤ 350 ms,
//     p95 ≤ 800 ms over 100 park→wake cycles)
//  4. idle-park                       — schedd reaper parks the live instance
//  5. second-request-wakes            — fresh wake from the same snapshot, asserts a new instance id
//
// All five share one Harness + one PG schema + one app — they tell the
// deploy→parked→wake→park→wake story sequentially. They run on t.Run
// without t.Parallel because they share state.
func TestDeployWakeMetal(t *testing.T) {
	// Pre-flight: metal env must be present, otherwise skip — the harness
	// would happily boot apid/schedd/imaged without /dev/kvm, but vmmd
	// would fail on its first ColdBoot. Skip early so the message is clear.
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal deploy→wake test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Fake registry on loopback. Stand it up BEFORE e2etest.Start so imaged's
	// startup-time `EnsureBaseExt4` can pull `onebox-faas/builder-base:latest`
	// from the same local registry (FAAS_OCI_INSECURE=1 lets it dial plain
	// HTTP). This avoids the production ghcr.io endpoint, which 403s for
	// anonymous pulls. The harness's imaged startup honors
	// FAAS_TEST_BUILDER_BASE_REF (see pkg/e2etest/harness.go) when set.
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	// Stub builder-base with a one-layer image — imaged's EnsureBaseExt4
	// rejects zero-layer bases ("manifest has no layers"). The deploy-time
	// base override (FAAS_TEST_DEPLOY_BASE_REF) points at a DIFFERENT repo
	// whose single layer's diff_id matches the app's hello.txt layer, so
	// oci.LayersAboveBase treats it as a base prefix and the app's hello
	// layer lands in `above` — exactly the two-drive shape imaged would
	// see with a real runner base + app diff.
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// The reference for the test's actual app image — digest-pinned. Use
	// the two-layer variant so oci.LayersAboveBase finds one above-base
	// layer after subtracting the deploy-base's single layer prefix.
	img, ref := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref = registry.AddImage("library/hello", img)

	// Create the app on Hobby plan (256 MB RAM cap, 2-concurrency cap).
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app"}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "hello")

	// Deploy. digest-pinned ref — imaged pulls, builds app layer, fires
	// snapshot_prime; schedd cold-boots via vmmd, snapshots, parks.
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/deployments",
		api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var depResp api.DeploymentResponse
	if err := json.Unmarshal(raw, &depResp); err != nil {
		t.Fatalf("decode deployment: %v body=%s", err, raw)
	}

	// apid stores the full ref (`host/repo@sha256:...`) into
	// deployments.image_digest so imaged's OCI puller can dial the
	// right registry host. imaged reads the row when it gets the
	// deployment_changed pg_notify. (Historically the column held
	// just the bare digest; that resolution collapsed every
	// non-Docker-Hub deploy to docker.io/library/sha256:..., so the
	// local fakeregistry was never reachable. Fixed by apid emitting
	// the full ref — see cmd/apid/handlers.go createDeployment.)

	var firstInstanceID string

	// -- 1. deploy-then-parked -------------------------------------------------
	t.Run("deploy-then-parked", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		// Dump daemon logs on every exit path of subtest 1 — the whole
		// point of this subtest on the Lima acceptance loop is the wire
		// evidence. Failure path logs the deployment state + daemon
		// output; success path logs the daemon output so a green run is
		// self-documenting in the test log.
		defer h.DumpLogs(t)
		dep, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depResp.ID, 60*time.Second)
		if err != nil {
			if d, derr := state.NewPgStore(pool).DeploymentByID(ctx, depResp.ID); derr == nil {
				t.Logf("deployment state at failure: status=%s error=%q", d.Status, d.Error)
			}
			t.Fatalf("deployment did not reach live: %v", err)
		}
		ins, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 60*time.Second)
		if err != nil {
			t.Fatalf("no parked instance: %v", err)
		}
		if len(ins) == 0 {
			t.Fatal("no instances after deploy")
		}
		if dep.Status != state.DeployLive {
			t.Errorf("dep.Status = %s, want live", dep.Status)
		}
		t.Logf("deploy-then-parked: dep=%s instance=%s parked", dep.ID, ins[0].ID)
	})

	// -- 2. first-request-wakes ------------------------------------------------
	t.Run("first-request-wakes", func(t *testing.T) {
		url := gatewayAppURL(h, "hello")
		client := h.HTTPClient()
		// gatewayd's host→app router is fed by app_changed pg_notify; spin
		// briefly so the cache is warm before the first request.
		if err := e2etest.WaitForHTTPReady(context.Background(), t, client, url, 5*time.Second); err != nil {
			t.Fatalf("gateway not ready: %v", err)
		}
		body, status := doGetWithHost(t, client, url, "hello.apps.test.example", 30*time.Second)
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if got := strings.TrimSpace(string(body)); got != helloBody {
			t.Fatalf("body=%q want %q", got, helloBody)
		}
		ins, err := e2etest.WaitForInstanceState(context.Background(), t, pool, appID, state.StateRunning, 10*time.Second)
		if err != nil {
			t.Fatalf("no running instance after wake: %v", err)
		}
		firstInstanceID = ins[0].ID

		// M8 §14: the wake-latency histogram must observe the cold wake
		// (Part A's first-byte RoundTripper — see pkg/gateway/wake_timing.go).
		// Scraping /metrics from the loopback control listener asserts the
		// metric shape end-to-end through the daemon, not just at the unit
		// level.
		assertWakeLatencyObserved(t, h.GatewayControlURL)
	})

	// -- 3. wake-latency-p50p95-100cycles ------------------------------------
	t.Run("wake-latency-p50p95-100cycles", func(t *testing.T) {
		// §14 V2 / spec §6.3 / Appendix D V2 gate: 100 park→wake cycles,
		// p50 ≤ 350 ms, p95 ≤ 800 ms over the gateway_wake_latency_seconds
		// histogram (request-received → first upstream byte, per Part A).
		//
		// The histogram is the load-bearing SLO signal; this test is the one
		// that asserts the on-the-wire number backs the dashboard's p50/p95
		// panels (deploy/grafana/faas-fleet.json:10-41). We compute quantiles
		// from the cumulative bucket counts using pkg/gateway/testhist
		// (standard PromQL histogram_quantile() interpolation) and assert
		// both p50 and p95 against the §6.3 budget.
		//
		// Wall-clock math: per cycle we wait `idle_timeout (10s) + reaper_tick
		// (10s) ≈ 20s` for park, then ~350ms for wake, then ~5ms state wait
		// for running — ~20.4s/cycle × 100 ≈ 34 min on EX44, ~50 min on Lima
		// nested-virt. The 60m `-timeout` set in run-metal.sh has ~10-25 min
		// of headroom.
		if h.GatewayControlURL == "" {
			t.Skip("gatewayd control URL not exposed by harness")
		}
		// Sanity: the histogram series must exist before we attempt to read it.
		// Subtest 2 already observed at least one sample; if we can't find it
		// here, the metric shape regressed.
		assertWakeLatencyObserved(t, h.GatewayControlURL)

		// Hobby's default idle timeout is 60 s + a 10 s reaper tick — too
		// slow for a 100-cycle loop (would push the run past the -timeout 60m
		// budget). Dial it down to the spec §4.3 floor (10 s) so each cycle
		// settles into parked within one reaper tick. This is a per-app knob
		// (PATCH /v1/apps/{slug}) and stays within api.IdleTimeoutFloorSeconds.
		setAppIdleTimeout(t, h, key, "hello", api.IdleTimeoutFloorSeconds)

		url := gatewayAppURL(h, "hello")
		client := h.HTTPClient()
		const cycles = 100
		const minObservations = 90 // allow some slack for reaper-timing edges
		loopStart := time.Now()
		for i := 0; i < cycles; i++ {
			// Wait until the reaper parks the instance. With idle=10s and
			// reaper tick=10s, worst case is ~20s; allow 30s for slack.
			parkStart := time.Now()
			parkCtx, parkCancel := context.WithTimeout(context.Background(), 30*time.Second)
			if _, err := e2etest.WaitForInstanceState(parkCtx, t, pool, appID, state.StateParked, 25*time.Second); err != nil {
				parkCancel()
				t.Fatalf("cycle %d: did not park within 25s (reaper lagging?): %v", i, err)
			}
			parkCancel()
			if parkDur := time.Since(parkStart); parkDur > 15*time.Second {
				t.Logf("cycle %d: slow park (%v > 15s) — reaper near tick boundary", i, parkDur)
			}
			body, status := doGetWithHost(t, client, url, "hello.apps.test.example", 30*time.Second)
			if status != http.StatusOK {
				t.Fatalf("cycle %d: status=%d body=%s", i, status, body)
			}
			if got := strings.TrimSpace(string(body)); got != helloBody {
				t.Fatalf("cycle %d: body=%q want %q", i, got, helloBody)
			}
			// Confirm the wake landed before the next cycle. The histogram
			// only emits a cold-wake sample when h.backend.Target returns
			// not-ready (handler.go:212), which is exactly the parked→wake
			// transition we just exercised.
			runCtx, runCancel := context.WithTimeout(context.Background(), 15*time.Second)
			if _, err := e2etest.WaitForInstanceState(runCtx, t, pool, appID, state.StateRunning, 10*time.Second); err != nil {
				runCancel()
				t.Fatalf("cycle %d: did not reach running after wake: %v", i, err)
			}
			runCancel()
		}
		t.Logf("loop completed in %v (%d cycles)", time.Since(loopStart), cycles)

		// Single scrape at the end — the histogram is a process-wide
		// accumulator; reading 100× and diffing would only buy us running
		// statistics (mean), not a defensible p50/p95. Pull the bucket
		// cumulative counts and compute the quantiles via PromQL-equivalent
		// interpolation.
		sc, err := scrapeHistogram(t, h.GatewayControlURL, "gateway_wake_latency_seconds")
		if err != nil {
			t.Fatalf("scrape histogram: %v", err)
		}
		// Subtract the prior-cycle observations: subtests 2 + 4 each observe
		// one cold wake (subtest 4 will observe one more in second-request-wakes,
		// but that runs after this subtest). So we expect ≈ cycles + 1 samples.
		// We accept ≥ minObservations to allow for occasional reaper misses
		// where the wake landed on a still-running instance (the histogram
		// only fires on cold path — handler.go:235).
		if sc.SampleCount < minObservations {
			t.Fatalf("histogram SampleCount = %d, want >= %d (some cycles hit hot instances; reaper too slow?)",
				sc.SampleCount, minObservations)
		}
		p50 := testhist.QuantileScrape(t, sc, 0.50)
		p95 := testhist.QuantileScrape(t, sc, 0.95)
		t.Logf("wake_latency over %d cycles: p50=%v p95=%v (samples=%d sum=%.3fs)",
			cycles, p50, p95, sc.SampleCount, sc.SampleSum)

		const p50Budget = 350 * time.Millisecond
		const p95Budget = 800 * time.Millisecond
		if p50 > p50Budget {
			t.Errorf("wake_latency p50 = %v, want <= %v (spec §6.3 / Appendix D V2)", p50, p50Budget)
		}
		if p95 > p95Budget {
			t.Errorf("wake_latency p95 = %v, want <= %v (spec §6.3 / Appendix D V2)", p95, p95Budget)
		}
	})

	// -- 4. idle-park ----------------------------------------------------------
	t.Run("idle-park", func(t *testing.T) {
		// schedd's reaper tick is hardcoded at 10 s in the daemon binary
		// (pkg/sched/loop.go:103). Hobby's idle timeout was dialled down to
		// api.IdleTimeoutFloorSeconds (=10s) by subtest 3 so the 100-cycle
		// loop is bounded; this subtest still exercises the reaper path
		// against that dialed-down value, not the original Hobby 60s.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ins, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 25*time.Second)
		if err != nil {
			t.Fatalf("instance did not re-park: %v", err)
		}
		if len(ins) == 0 {
			t.Fatal("no instances after re-park")
		}
		if ins[0].ID != firstInstanceID {
			t.Errorf("re-park created a new instance %s; should reuse %s", ins[0].ID, firstInstanceID)
		}
	})

	// -- 5. second-request-wakes -----------------------------------------------
	t.Run("second-request-wakes", func(t *testing.T) {
		url := gatewayAppURL(h, "hello")
		client := h.HTTPClient()
		body, status := doGetWithHost(t, client, url, "hello.apps.test.example", 30*time.Second)
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if got := strings.TrimSpace(string(body)); got != helloBody {
			t.Fatalf("body=%q want %q", got, helloBody)
		}
		ins, err := e2etest.WaitForInstanceState(context.Background(), t, pool, appID, state.StateRunning, 10*time.Second)
		if err != nil {
			t.Fatalf("no running instance after 2nd wake: %v", err)
		}
		if ins[0].ID == firstInstanceID {
			t.Errorf("second wake reused instance %s; should be a fresh instance", firstInstanceID)
		}
	})
}

// mustGetAppID fetches the app by slug and returns its id.
func mustGetAppID(t *testing.T, h *e2etest.Harness, key, slug string) string {
	t.Helper()
	raw, status := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug, nil)
	if status != http.StatusOK {
		t.Fatalf("get app: status=%d body=%s", status, raw)
	}
	var resp api.AppResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode app: %v body=%s", err, raw)
	}
	return resp.ID
}

// gatewayAppURL returns the gatewayd URL the harness booted, with the path
// appended. gatewayd's router does host-based lookup; the test sets the Host
// header explicitly (see doGetWithHost) since no DNS is wired up.
func gatewayAppURL(h *e2etest.Harness, _ string) string {
	return h.GatewayURL + "/"
}

// doGetWithHost fires GET url with a Host header of host so gatewayd's
// pgRouter routes by hostname (not by loopback IP).
func doGetWithHost(t *testing.T, client *http.Client, url, host string, timeout time.Duration) ([]byte, int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Host = host
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("GET %s (Host=%s): %v", url, host, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode
}

// setAppIdleTimeout issues PATCH /v1/apps/{slug} with the given idle
// timeout in seconds. Used by the 100-cycle wake-latency subtest to dial
// Hobby's 60s default down to api.IdleTimeoutFloorSeconds (10s) so the
// reaper settles each cycle within one tick — bounded test wall-clock.
func setAppIdleTimeout(t *testing.T, h *e2etest.Harness, key, slug string, secs int) {
	t.Helper()
	secsCopy := secs
	body := api.UpdateAppRequest{IdleTimeoutS: &secsCopy}
	raw, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, body)
	if status != http.StatusOK {
		t.Fatalf("set idle timeout: status=%d body=%s", status, raw)
	}
}

// assertWakeLatencyObserved scrapes /metrics from the gatewayd control
// listener and asserts the wake_latency histogram emitted at least one
// sample. This is the M8 §14 regression gate for "wake latency is measured
// end-to-end, not just at the unit level". Pre-Part-A the histogram would
// still emit a series but with the wrong observation (full body duration);
// post-Part-A it captures first-upstream-byte via the wake_timing.go
// RoundTripper (see handler.go:220-235).
//
// The histogram is unlabelled (pkg/gateway/metrics.go:48-55), so the
// assertions are on the bare series name. Pre-PR-70 the regex here looked
// for app_id=… labels which the histogram never carried; that regex was
// wrong and this subtest's reject path was the only thing that surfaced it.
//
// Single GET per call: the body is read once and passed to SnapshotFromText,
// not fetched twice (an earlier version called scrapeHistogram after reading
// the body, doing a second http.Get and discarding the first).
func assertWakeLatencyObserved(t *testing.T, controlURL string) {
	t.Helper()
	if controlURL == "" {
		t.Skip("gatewayd control URL not exposed by harness")
	}
	text := scrapeMetricsBody(t, controlURL)
	sc, err := testhist.SnapshotFromText(text, "gateway_wake_latency_seconds")
	if err != nil {
		t.Fatalf("assertWakeLatencyObserved: %v\n/metrics excerpt:\n%s", err, excerpt(text))
	}
	if sc.SampleCount < 1 {
		t.Fatalf("wake_latency SampleCount = %d, want >= 1", sc.SampleCount)
	}
	// At least one bucket must have advanced — empty histograms emit a
	// series with no _bucket lines, which is what a buggy non-emitting
	// histogram would look like.
	if len(sc.BucketLE) == 0 {
		t.Fatalf("wake_latency histogram has no _bucket lines; observation may not be first-byte (Part A regression)")
	}
}

// scrapeHistogram fetches /metrics and parses the named histogram into a
// *testhist.ScrapeHistogram. Used by the post-loop p50/p95 assertion.
func scrapeHistogram(t *testing.T, controlURL, metricName string) (*testhist.ScrapeHistogram, error) {
	t.Helper()
	text := scrapeMetricsBody(t, controlURL)
	return testhist.SnapshotFromText(text, metricName)
}

// scrapeMetricsBody is the single-GET helper that backs both
// assertWakeLatencyObserved and scrapeHistogram. Returning the raw text lets
// callers pass it to SnapshotFromText without a second fetch.
func scrapeMetricsBody(t *testing.T, controlURL string) string {
	t.Helper()
	resp, err := http.Get(controlURL + "/metrics")
	if err != nil {
		t.Fatalf("scrape /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("/metrics status=%d body=%s", resp.StatusCode, excerpt(string(body)))
	}
	return string(body)
}

// excerpt trims a /metrics body for failure logs. Prometheus exposition can
// run to several KB; the first 1 KB is plenty to identify a missing series.
func excerpt(s string) string {
	const max = 1024
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
