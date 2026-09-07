//go:build metal

// wake_timeline_metal_test.go — issue #517 PR-C / ADR-064 — M5 §14
// acceptance: the customer-facing wake-timeline endpoint exposes
// every stage of a cold wake as a queryable, ordered timeline.
//
// Acceptance criteria (issue #517 AC2):
//
//   - A cold wake has a queryable timeline with queue, admission,
//     restore/cold-boot, guest resume, readiness, and proxy stages.
//
// The test deploys a Hobby-plan app, lets it park, fires one
// gatewayd request (which cold-wakes the snapshot), captures the
// x-faas-wake-id response header, then queries the new
// /v1/apps/{slug}/wakes/{wake_id}/timeline endpoint. The response
// must contain the canonical six-stage vocabulary in `at ASC` order:
//
//	wake.queue_accepted
//	wake.admitted
//	wake.boot_started
//	wake.boot_completed
//	wake.readiness_200
//	wake.proxy_first_byte
//
// Build tag: metal. Requires /dev/kvm + root (jailer needs
// CAP_NET_ADMIN, CAP_MKNOD, …), Firecracker on PATH, and
// FAAS_TEST_KERNEL pointing at a vmlinux. Runs on the dev EX44
// via `make test-metal`, or locally on M3+ Mac via
// `make metal-lima`.
package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// expectedWakeStages is the canonical AC2 vocabulary in expected
// at-ASC order. The handler orders by at ASC (oldest-first), so
// the first row must be queue_accepted, the last must be
// proxy_first_byte (the customer-facing terminal stage).
//
// The order is load-bearing: the canonical "forward narrative"
// the dashboard reads is queue → admit → boot → readiness →
// proxy. A wake stanza that arrives at the timeline in any other
// order would mean the events timestamps are being stamped
// wrong upstream (pkg/events.Platform stamps time.Now() at Emit,
// not the typed EmitAt — both are documented in ADR-064).
var expectedWakeStages = []string{
	"wake.queue_accepted",
	"wake.admitted",
	"wake.boot_started",
	"wake.boot_completed",
	"wake.readiness_200",
	"wake.proxy_first_byte",
}

// TestWakeTimelineMetal runs the AC2 acceptance:
//
//  1. deploy-then-parked       — apid → imaged → schedd → vmmd → parked
//  2. first-request-wakes      — gatewayd request triggers a cold wake;
//     capture x-faas-wake-id from the response header
//  3. timeline-after-wake      — GET /v1/apps/{slug}/wakes/{wake_id}/timeline
//     returns the canonical six-stage vocabulary in at-ASC order
//
// All three share one Harness + one PG schema + one app — they
// tell the wake-id → timeline round-trip story sequentially.
func TestWakeTimelineMetal(t *testing.T) {
	// Pre-flight: metal env must be present, otherwise skip — the
	// harness would happily boot apid/schedd/imaged without
	// /dev/kvm, but vmmd would fail on its first ColdBoot. Skip
	// early so the message is clear.
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal wake-timeline test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Fake registry on loopback. Same setup as TestDeployWakeMetal
	// (see cmd/e2e/deploy_wake_metal_test.go:71-95) — the harness's
	// imaged startup pulls onebox-faas/builder-base from this
	// registry, which serves the empty-layer base. The deploy
	// base ref is a different one-layer image whose diff_id
	// matches the app's hello.txt layer, so oci.LayersAboveBase
	// finds one above-base layer after the prefix subtraction.
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Deploy the reference app. Same two-layer image used by
	// TestDeployWakeMetal so the harness's seed/fixture is
	// identical and the wake stages match the §14 SLO.
	img, ref := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref = registry.AddImage("library/hello", img)

	// Issue #695 / ADR-080: Hobby defaults to require_authn=true
	// post-flip. The wake-timeline probe is anonymous; opt out at
	// create time so the routed GET continues to land on the app.
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}

	// -- 1. deploy-then-parked -------------------------------------------------
	t.Run("deploy-then-parked", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		defer h.DumpLogs(t)
		raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/deployments",
			api.CreateDeploymentRequest{Image: ref})
		if status != http.StatusAccepted {
			t.Fatalf("create deployment: status=%d body=%s", status, raw)
		}
		var depResp api.DeploymentResponse
		if err := json.Unmarshal(raw, &depResp); err != nil {
			t.Fatalf("decode deployment: %v body=%s", err, raw)
		}
		dep, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depResp.ID, 60*time.Second)
		if err != nil {
			t.Fatalf("deployment did not reach live: %v", err)
		}
		if dep.Status != state.DeployLive {
			t.Errorf("dep.Status = %s, want live", dep.Status)
		}
		appID := mustGetAppID(t, h, key, "hello")
		_, err = e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 60*time.Second)
		if err != nil {
			t.Fatalf("no parked instance: %v", err)
		}
	})

	// -- 2. first-request-wakes (capture x-faas-wake-id) ----------------------
	var wakeID string
	t.Run("first-request-wakes", func(t *testing.T) {
		url := gatewayAppURL(h, "hello")
		client := h.HTTPClient()
		if err := e2etest.WaitForHTTPReady(context.Background(), t, client, url, 5*time.Second); err != nil {
			t.Fatalf("gateway not ready: %v", err)
		}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("new req: %v", err)
		}
		req.Host = "hello.apps.test.example"
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := client.Do(req.WithContext(ctx))
		if err != nil {
			t.Fatalf("GET /: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.StatusCode, body)
		}

		// gatewayd sets x-faas-wake-id on the admitted path
		// (pkg/gateway/handler.go:707). It is the canonical join
		// key for the wake-timeline endpoint — the dashboard
		// reads it from the response and constructs the timeline
		// URL from the wake_id alone.
		wakeID = resp.Header.Get("x-faas-wake-id")
		if wakeID == "" {
			t.Fatalf("x-faas-wake-id header missing on wake response")
		}
		t.Logf("captured x-faas-wake-id=%s", wakeID)
	})

	// -- 3. timeline-after-wake ------------------------------------------------
	t.Run("timeline-after-wake", func(t *testing.T) {
		if wakeID == "" {
			t.Skip("wake_id not captured (subtest 2 failed)")
		}
		// The events Platform.Emit is async-best-effort (PR-C
		// commit 1): a wake that completes in <1ms can race the
		// emit goroutine. Poll the timeline endpoint briefly
		// until all six stages have landed, or fail with the
		// observed count so the wire evidence is on the failure
		// path.
		var resp api.WakeTimelineResponse
		deadline := time.Now().Add(5 * time.Second)
		var observed []string
		for time.Now().Before(deadline) {
			raw, status := doReq(t, h, key, http.MethodGet,
				"/v1/apps/hello/wakes/"+wakeID+"/timeline", nil)
			if status != http.StatusOK {
				t.Fatalf("timeline: status=%d body=%s", status, raw)
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				t.Fatalf("decode timeline: %v body=%s", err, raw)
			}
			if len(resp.Events) >= len(expectedWakeStages) {
				break
			}
			observed = observedOrAll(resp.Events)
			time.Sleep(50 * time.Millisecond)
		}
		// Validate at-asc ordering. The handler orders by `at
		// ASC` (oldest-first), so the canonical "forward
		// narrative" the dashboard reads is queue → admit → boot
		// → readiness → proxy. A wake stanza that arrives at
		// the timeline in any other order is a wire-evidence
		// bug — pkg/events stamps time.Now() at Emit, not the
		// typed EmitAt, so emit-site ordering is the only
		// guarantee.
		for i, want := range expectedWakeStages {
			if i >= len(resp.Events) {
				t.Fatalf("timeline missing stages: got %d, want %d (observed so far: %v)",
					len(resp.Events), len(expectedWakeStages), observed)
			}
			if resp.Events[i].Kind != want {
				t.Fatalf("event[%d].kind = %q, want %q (at-ASC order broken)",
					i, resp.Events[i].Kind, want)
			}
		}
		// Confirm the wake_id on the response matches what
		// gatewayd emitted (forge-proof: a different response
		// would imply the response is from a stale or wrong
		// wake).
		if resp.WakeID != wakeID {
			t.Errorf("resp.WakeID = %q, want %q", resp.WakeID, wakeID)
		}
		// Forge-proof: the resolved app_id must be the
		// slug's app, not the zero-value.
		if resp.AppID == "" {
			t.Errorf("resp.AppID empty, want the slug's app id")
		}
		t.Logf("timeline: %d frames in at-ASC order, wake_id=%s", len(resp.Events), wakeID)
	})
}

// observedOrAll returns the kinds of the events whose index is
// past the observed set, or all kinds if no events have been
// observed yet. Used to populate the failure log so the on-call
// can see which stages were still missing.
func observedOrAll(events []api.WakeTimelineEvent) []string {
	if len(events) == 0 {
		return []string{"<none>"}
	}
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}
