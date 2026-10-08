//go:build metal

// crash_snapshot_metal_test.go — ADR-733 crash snapshots end to end on real
// Firecracker: apid requests a capture of the running instance, schedd's
// CrashCaptureCoordinator captures it in place (the instance keeps serving
// and no snapshots row appears), and the capture opens as an ADR-732 fork
// that a token-bearing request reaches through the gateway.
//
// Requires /dev/kvm, root, Firecracker on PATH and FAAS_TEST_KERNEL.
package e2e_test

// adr: 733

import (
	"context"
	"encoding/json"
	"errors"
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

func TestCrashSnapshotMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal crash snapshot test")
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

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_APP_FORKS=1", "FAAS_CRASH_SNAPSHOTS=1")
	key := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "hello")
	// Pro defaults to bearer public auth (ADR-079); the anonymous probes
	// below exercise routing, not edge auth.
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
	serving, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 30*time.Second)
	if err != nil || len(serving) == 0 {
		t.Fatalf("no running serving instance: %v", err)
	}

	var capture api.CrashCaptureResponse
	t.Run("capture-in-place", func(t *testing.T) {
		raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/crash-snapshots", nil)
		if status != http.StatusAccepted {
			t.Fatalf("request capture = %d %s", status, raw)
		}
		if err := json.Unmarshal(raw, &capture); err != nil {
			t.Fatal(err)
		}
		capture = waitCrashCaptureStatus(ctx, t, h, key, capture.ID, "ready", 120*time.Second)
		ins, err := state.NewPgStore(pool).InstanceByID(ctx, serving[0].ID)
		if err != nil || ins.State != string(state.StateRunning) {
			t.Fatalf("serving instance after capture = %+v, %v; want still running", ins, err)
		}
		// Only the deploy's own captures may exist as snapshots rows; the crash
		// capture key must not be among them.
		for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
			snap, err := state.NewPgStore(pool).LatestSnapshotForTier(ctx, dep.ID, tier)
			if err == nil && strings.Contains(snap.StorageKey, capture.ID) {
				t.Fatalf("crash capture became a %s snapshots row: %s", tier, snap.StorageKey)
			} else if err != nil && !errors.Is(err, state.ErrNotFound) {
				t.Fatal(err)
			}
		}
	})

	t.Run("open-as-fork-and-reach-it", func(t *testing.T) {
		raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/crash-snapshots/"+capture.ID+"/fork", api.CreateAppForkRequest{})
		if status != http.StatusAccepted {
			t.Fatalf("fork capture = %d %s", status, raw)
		}
		var fork api.AppForkResponse
		if err := json.Unmarshal(raw, &fork); err != nil || fork.AccessToken == "" {
			t.Fatalf("decode fork: %v %s", err, raw)
		}
		waitForkStatus(ctx, t, h, key, fork.ID, "running", 120*time.Second)
		stored, err := state.NewPgStore(pool).AppForkForApp(ctx, appID, fork.ID)
		if err != nil || stored.CrashCaptureID == nil || *stored.CrashCaptureID != capture.ID {
			t.Fatalf("fork row = %+v, %v; want pinned to the capture", stored, err)
		}
		headers, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/", nil,
			map[string]string{api.ForkHeader: fork.ID, api.ForkTokenHeader: fork.AccessToken})
		if status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody || headers.Get("X-Gregale-Fork-Served") != "1" {
			t.Fatalf("request to the crash fork = %d %q", status, body)
		}
	})
}

func waitCrashCaptureStatus(ctx context.Context, t *testing.T, h *e2etest.Harness, key, id, want string, timeout time.Duration) api.CrashCaptureResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last api.CrashCaptureResponse
	for time.Now().Before(deadline) && ctx.Err() == nil {
		raw, status := doReq(t, h, key, http.MethodGet, "/v1/apps/hello/crash-snapshots/"+id, nil)
		if status == http.StatusOK && json.Unmarshal(raw, &last) == nil {
			if last.Status == want {
				return last
			}
			if last.Failure != nil {
				t.Fatalf("capture %s failed: %s %s", id, last.Failure.Code, last.Failure.Message)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("capture %s status = %q after %s, want %q", id, last.Status, timeout, want)
	return last
}
