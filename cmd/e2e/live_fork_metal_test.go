//go:build metal

// live_fork_metal_test.go — ADR-732 live forks end to end on real
// Firecracker: the serving instance's in-memory counter is bumped, a normal
// fork (the deployment's snapshot) shows the old value, and a live fork
// (captured when created) shows the current one, while the serving
// instance keeps running.
//
// Requires /dev/kvm, root, Firecracker on PATH and FAAS_TEST_KERNEL.
package e2e_test

// adr: 732

import (
	"context"
	"encoding/json"
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

func TestLiveForkMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal live fork test")
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
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_APP_FORKS=1", "FAAS_CRASH_SNAPSHOTS=1",
		"FAAS_HOST_AGE_IDENTITY_PATH="+writeE2EAgeKeys(t))
	key := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.CrashCaptureImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "hello")
	stopAppOnCleanup(t, h, pool, key, "hello", appID)
	if body, code := doReq(t, h, key, http.MethodPatch, "/v1/apps/hello", map[string]any{
		"require_authn": false, "public_auth": map[string]any{"mode": "open"},
	}); code != http.StatusOK {
		t.Fatalf("open public auth: %d %s", code, body)
	}
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	defer h.DumpLogs(t)
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 90*time.Second); err != nil {
		t.Fatalf("deployment not live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(ctx, t, client, gatewayAppURL(h, "hello"), 10*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	if body, status := doGetWithHost(t, client, gatewayAppURL(h, "hello"), "hello.apps.test.example", 60*time.Second); status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
		t.Fatalf("serving wake = %d %q", status, body)
	}
	for range 3 {
		if _, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodPost, "/counter", nil); status != http.StatusOK {
			t.Fatalf("bump counter = %d %s", status, body)
		}
	}
	serving, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 10*time.Second)
	if err != nil || len(serving) == 0 {
		t.Fatalf("no running serving instance: %v", err)
	}

	forkCounter := func(t *testing.T, fork api.AppForkResponse) int {
		t.Helper()
		_, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/counter", nil,
			map[string]string{api.ForkHeader: fork.ID, api.ForkTokenHeader: fork.AccessToken})
		var got struct {
			Counter int `json:"counter"`
		}
		if status != http.StatusOK || json.Unmarshal(body, &got) != nil {
			t.Fatalf("fork /counter = %d %s", status, body)
		}
		return got.Counter
	}
	openFork := func(t *testing.T, req api.CreateAppForkRequest) api.AppForkResponse {
		t.Helper()
		raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/forks", req)
		if status != http.StatusAccepted {
			t.Fatalf("create fork = %d %s", status, raw)
		}
		var fork api.AppForkResponse
		if err := json.Unmarshal(raw, &fork); err != nil || fork.AccessToken == "" {
			t.Fatalf("decode fork: %v %s", err, raw)
		}
		waitForkStatus(ctx, t, h, key, fork.ID, "running", 120*time.Second)
		return fork
	}
	closeFork := func(t *testing.T, fork api.AppForkResponse) {
		t.Helper()
		if _, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/hello/forks/"+fork.ID, nil); status != http.StatusAccepted {
			t.Fatalf("cancel fork = %d", status)
		}
		waitForkStatus(ctx, t, h, key, fork.ID, "cancelled", 60*time.Second)
	}

	t.Run("snapshot-fork-has-the-deploy-time-state", func(t *testing.T) {
		fork := openFork(t, api.CreateAppForkRequest{})
		if fork.CrashCaptureID != nil {
			t.Fatalf("normal fork pinned to capture %s", *fork.CrashCaptureID)
		}
		if n := forkCounter(t, fork); n != 0 {
			t.Fatalf("snapshot fork counter = %d, want 0", n)
		}
		closeFork(t, fork)
	})

	t.Run("live-fork-has-the-current-state", func(t *testing.T) {
		yes := true
		fork := openFork(t, api.CreateAppForkRequest{Live: &yes})
		if fork.CrashCaptureID == nil {
			t.Fatal("live fork is not pinned to a capture")
		}
		if n := forkCounter(t, fork); n != 3 {
			t.Fatalf("live fork counter = %d, want 3", n)
		}
		capture := waitCrashCaptureStatus(ctx, t, h, key, *fork.CrashCaptureID, "ready", 10*time.Second)
		if capture.Trigger != "live_fork" || capture.ExpiresAt == nil {
			t.Fatalf("capture = %+v, want a ready live_fork capture", capture)
		}
		if exp, err := time.Parse(time.RFC3339Nano, *capture.ExpiresAt); err != nil || time.Until(exp) > api.LiveForkCaptureRetention {
			t.Fatalf("capture expires_at = %s, want within %v", *capture.ExpiresAt, api.LiveForkCaptureRetention)
		}
		ins, err := state.NewPgStore(pool).InstanceByID(ctx, serving[0].ID)
		if err != nil || ins.State != string(state.StateRunning) {
			t.Fatalf("serving instance after the live capture = %+v, %v; want still running", ins, err)
		}
		if _, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/counter", nil); status != http.StatusOK || !strings.Contains(string(body), `"counter":3`) {
			t.Fatalf("serving /counter after the live fork = %d %s", status, body)
		}
		closeFork(t, fork)
	})
}
