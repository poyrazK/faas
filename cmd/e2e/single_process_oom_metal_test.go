//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSingleProcessOOMMetal drives the ADR-121 amendment through real VMs. A
// single-process app (no declared workload, so no in-guest memory cap) that
// the guest kernel kills for memory:
//
//   - while its release is live: the kill is reported (peak and the
//     instance's plan RAM), the instance is replaced, and the release keeps
//     serving;
//   - during its first boot: the deployment fails with app_runtime_oom
//     instead of a generic readiness failure.
func TestSingleProcessOOMMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping single-process OOM acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping single-process OOM acceptance")
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "full-rootfs")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	image, _ := e2etest.HelloImage("library/oom-app", "hello from oom fixture")
	ref := registry.AddImage("library/oom-app", image)
	store := state.NewPgStore(pool)

	t.Run("live release keeps serving", func(t *testing.T) {
		const slug = "oom-live"
		appID, depID := createOOMFixtureApp(t, h, key, slug, ref, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 90*time.Second); err != nil {
			t.Fatalf("deployment did not reach live: %v", err)
		}
		if body, status := doGetWithHost(t, h.HTTPClient(), gatewayAppURL(h, slug), slug+".apps.test.example", 60*time.Second); status != http.StatusOK {
			t.Fatalf("first request: status=%d body=%s", status, body)
		}
		app, err := store.AppByID(ctx, appID)
		if err != nil {
			t.Fatalf("read app: %v", err)
		}

		// The request dies with its instance; its status is not the subject.
		postToGateway(t, h, slug, "__fixture/oom")
		payload := waitOOMEvent(ctx, t, pool, appID, 90*time.Second)
		if payload.DeploymentID != depID || payload.DeploymentFailed {
			t.Fatalf("OOM event = %+v, want deployment %s not failed", payload, depID)
		}
		if payload.PeakMB <= 0 || payload.PlanMB != app.RAMMB {
			t.Fatalf("OOM event peak=%d plan=%d, want a peak and the app's %d MB", payload.PeakMB, payload.PlanMB, app.RAMMB)
		}
		dep, err := store.DeploymentByID(ctx, depID)
		if err != nil {
			t.Fatalf("read deployment: %v", err)
		}
		if dep.Status != state.DeployLive || dep.ErrorCode != "" || dep.TrafficPercent != 100 {
			t.Fatalf("live release after OOM: status=%s error_code=%q traffic=%d, want live at 100%% with no error", dep.Status, dep.ErrorCode, dep.TrafficPercent)
		}
		if body, status := doGetWithHost(t, h.HTTPClient(), gatewayAppURL(h, slug), slug+".apps.test.example", 90*time.Second); status != http.StatusOK {
			t.Fatalf("request after OOM: status=%d body=%s", status, body)
		}
		parkOOMFixtureApp(t, h, pool, key, slug, appID)
	})

	t.Run("startup OOM fails the deployment", func(t *testing.T) {
		const slug = "oom-start"
		_, depID := createOOMFixtureApp(t, h, key, slug, ref, map[string]string{"FIXTURE_OOM_AT_START": "1"})
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		dep, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 3*time.Minute)
		if err == nil {
			t.Fatal("deployment that exhausts memory at startup reached live")
		}
		if dep.Status != state.DeployFailed || dep.ErrorCode != api.CodeAppRuntimeOOM {
			t.Fatalf("startup OOM deployment: status=%s error_code=%q error=%q, want failed %s", dep.Status, dep.ErrorCode, dep.Error, api.CodeAppRuntimeOOM)
		}
	})
}

func createOOMFixtureApp(t *testing.T, h *e2etest.Harness, key, slug, ref string, env map[string]string) (string, string) {
	t.Helper()
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app %s: status=%d", slug, got)
	}
	request := api.CreateDeploymentRequest{Image: ref}
	if env != nil {
		request.Overrides = &api.CreateDeploymentOverrides{Env: env}
	}
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", request)
	if status != http.StatusAccepted {
		t.Fatalf("create deployment %s: status=%d body=%s", slug, status, body)
	}
	return mustGetAppID(t, h, key, slug), parseImageDeployment(t, body)
}

func postToGateway(t *testing.T, h *e2etest.Harness, slug, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayAppURL(h, slug)+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = slug + ".apps.test.example"
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		t.Logf("POST /%s: %v (expected: the instance was killed)", path, err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	t.Logf("POST /%s: status=%d body=%.200s", path, resp.StatusCode, body)
}

type oomEventPayload struct {
	DeploymentID     string `json:"deployment_id"`
	PeakMB           int    `json:"peak_mb"`
	PlanMB           int    `json:"plan_mb"`
	DeploymentFailed bool   `json:"deployment_failed"`
}

func waitOOMEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, appID string, deadline time.Duration) oomEventPayload {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		var raw []byte
		err := pool.QueryRow(ctx, `SELECT data FROM events
			 WHERE kind = 'instances.workload_oom_failed' AND data ? 'app_id' AND data->>'app_id' = $1
			 ORDER BY at DESC, id DESC LIMIT 1`, appID).Scan(&raw)
		if err == nil {
			var payload oomEventPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("decode OOM event %s: %v", raw, err)
			}
			return payload
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("read OOM event: %v", err)
		}
		if time.Now().After(end) {
			t.Fatalf("no instances.workload_oom_failed event for app %s within %s", appID, deadline)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func parkOOMFixtureApp(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key, slug, appID string) {
	t.Helper()
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
		t.Fatalf("park %s: status=%d", slug, status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
		t.Fatalf("park %s: %v", slug, err)
	}
}
