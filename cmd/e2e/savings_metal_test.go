//go:build metal

// savings_metal_test.go — the scale-to-zero savings estimate end to end on
// real Firecracker: an app deploys, parks, wakes on a request, meterd
// bills its running seconds into usage_minutes, and both
// GET /v1/apps/{slug}/savings and `gregale usage savings` report that
// usage against the always-on baseline.
//
// Requires /dev/kvm, root, Firecracker on PATH and FAAS_TEST_KERNEL, like
// deploy_wake_metal_test.go.
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSavingsMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal savings test")
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

	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.Meterd, "FAAS_SAMPLE_INTERVAL=2s")
	key := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)

	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "hello")
	// vmmd keeps VMs running across a restart, so idle-park the woken
	// instance before the harness stops or its microVM leaks on the host.
	t.Cleanup(func() {
		setAppIdleTimeout(t, h, key, "hello", api.IdleTimeoutFloorSeconds)
		parkCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := e2etest.WaitForAppParked(parkCtx, t, pool, appID, 60*time.Second); err != nil {
			t.Errorf("teardown: app not parked, its microVM will leak: %v", err)
		}
	})
	// Pro defaults to bearer public auth (ADR-079); the wake below is anonymous.
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
		t.Fatalf("wake = %d %q", status, body)
	}

	var api1 api.AppSavingsResponse
	t.Run("api-reports-billed-usage", func(t *testing.T) {
		api1 = waitForSavedUsage(ctx, t, h, key)
		if api1.BillableRAMMB <= 0 {
			t.Errorf("billable_ram_mb = %d", api1.BillableRAMMB)
		}
		if api1.AlwaysOnMBSeconds < api1.ActualMBSeconds || api1.SavedMBSeconds != api1.AlwaysOnMBSeconds-api1.ActualMBSeconds {
			t.Errorf("inconsistent figures: %+v", api1)
		}
		now := time.Now().UTC()
		if api1.PeriodEnd.After(now) || now.Sub(api1.PeriodEnd) > time.Minute {
			t.Errorf("period_end = %v, want now (%v)", api1.PeriodEnd, now)
		}
		if hour := now.Truncate(time.Hour); api1.BaselineStart.Before(hour.Add(-time.Hour)) || api1.BaselineStart.After(now) {
			t.Errorf("baseline_start = %v, want the first billed hour (around %v)", api1.BaselineStart, hour)
		}
		if api1.ParkedRatio < 0 || api1.ParkedRatio > 1 || api1.SavedMillicents < 0 {
			t.Errorf("ratio/money out of range: %+v", api1)
		}
	})

	t.Run("cli-renders-the-estimate", func(t *testing.T) {
		cli := gregaleCLIForE2E(t)
		run := func(args ...string) []byte {
			t.Helper()
			runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(runCtx, cli, args...)
			cmd.Env = append(os.Environ(), "FAAS_API="+h.APIDURL, "FAAS_TOKEN="+key)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("gregale %v: %v\n%s", args, err, out)
			}
			return out
		}
		var got api.AppSavingsResponse
		if err := json.Unmarshal(run("--json", "usage", "savings", "--app", "hello"), &got); err != nil {
			t.Fatalf("decode CLI json: %v", err)
		}
		if got.Slug != "hello" || got.ActualMBSeconds < api1.ActualMBSeconds {
			t.Fatalf("CLI savings = %+v, want hello with actual >= %d", got, api1.ActualMBSeconds)
		}
		text := string(run("usage", "savings", "--app", "hello"))
		for _, want := range []string{"hello", "Estimate"} {
			if !strings.Contains(text, want) {
				t.Errorf("CLI output missing %q:\n%s", want, text)
			}
		}
	})
}

// waitForSavedUsage polls the savings endpoint until meterd has billed the
// woken instance into usage_minutes.
func waitForSavedUsage(ctx context.Context, t *testing.T, h *e2etest.Harness, key string) api.AppSavingsResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		raw, status := doReq(t, h, key, http.MethodGet, "/v1/apps/hello/savings", nil)
		if status != http.StatusOK {
			t.Fatalf("savings = %d %s", status, raw)
		}
		var out api.AppSavingsResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if out.ActualMBSeconds > 0 {
			return out
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("no billed usage after 3m: %+v", out)
		}
		time.Sleep(2 * time.Second)
	}
}

// gregaleCLIForE2E returns a gregale binary: the prebuilt one in
// FAAS_E2E_BIN_DIR when present, otherwise a fresh build.
func gregaleCLIForE2E(t *testing.T) string {
	t.Helper()
	if dir := strings.TrimSpace(os.Getenv("FAAS_E2E_BIN_DIR")); dir != "" {
		if path := filepath.Join(dir, "gregale"); fileExists(path) {
			return path
		}
	}
	path := filepath.Join(t.TempDir(), "gregale")
	out, err := exec.Command("go", "build", "-o", path, "github.com/onebox-faas/faas/cmd/gregale").CombinedOutput()
	if err != nil {
		t.Fatalf("build gregale: %v\n%s", err, out)
	}
	return path
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
