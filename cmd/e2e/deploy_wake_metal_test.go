//go:build metal

// deploy_wake_metal_test.go — M5 §14 acceptance: faas deploy → parked → first
// request wakes.
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
	"github.com/onebox-faas/faas/pkg/state"
)

// helloBody is the round-trip fixture: the bytes written into app/hello.txt
// (intentionally without a trailing newline — `cat` would echo one if we kept
// it, and the body/trim assertion `strings.TrimSpace(body) == helloBody` is
// comparing the *content* against the trimmed form). Keeping the fixture
// newline-free makes the assertion robust against CRLF or transport-layer
// wrapping on the wire.
const helloBody = "hello from faas"

// TestDeployWakeMetal runs the four-subtest acceptance:
//
//  1. deploy-then-parked       — apid → imaged → schedd → vmmd → parked
//  2. first-request-wakes      — gatewayd request triggers a wake from snapshot
//  3. idle-park                — schedd reaper parks the live instance
//  4. second-request-wakes     — fresh wake from the same snapshot, asserts a new instance id
//
// All four share one Harness + one PG schema + one app — they tell the
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
	// Stub builder-base — an empty layer is enough; imaged just stages an
	// ext4 from the layers, and M5 acceptance never actually boots the
	// builder VM (builderd is M6).
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")

	h := e2etest.Start(t, pool, e2etest.All)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// The reference for the test's actual app image — digest-pinned.
	img, ref := e2etest.HelloImage("library/hello", helloBody)
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

	var firstInstanceID string

	// -- 1. deploy-then-parked -------------------------------------------------
	t.Run("deploy-then-parked", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		dep, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depResp.ID, 60*time.Second)
		if err != nil {
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
	})

	// -- 3. idle-park ----------------------------------------------------------
	t.Run("idle-park", func(t *testing.T) {
		// schedd's reaper tick is hardcoded at 10 s in the daemon binary —
		// no env override exists. Hobby's idle timeout is 60 s, so the
		// reaper picks up last_request_at ~60 s after the wake in subtest 2.
		// Wait up to 75 s for the natural timeout. That's slow, but it's
		// correct — the alternative is a fake clock which would skip the
		// test of the real reaper path.
		//
		// TODO: schedd should accept FAAS_REAPER_INTERVAL_MS + a clock seam
		// so tests don't pay this latency. Filed as a follow-up.
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		ins, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 70*time.Second)
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

	// -- 4. second-request-wakes -----------------------------------------------
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
