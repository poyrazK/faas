//go:build metal

// shared_runtime_rollback_metal_test.go — ADR-1500 slice 4 acceptance
// (ADR-20261028090000): rollback across the two Node app layer kinds.
//
// A full-layer deployment (base-minimal drive0, the toolchain in drive1) and
// a shared-runtime deployment (runner-node22 drive0, an /app-only drive1)
// of one app must each serve after a rollback onto it, from a cold boot and
// from a snapshot restore. Each deployment boots the base it recorded, not
// the app's current one.
package e2e_test

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

const (
	fullLayerBody     = "hello from faas (full layer)"
	sharedRuntimeBody = "hello from faas (shared runtime)"
)

// layerKindFixture is a Node 22 app answering body. A prestart hook keeps the
// build on the full-layer path (sharedruntime.ReasonNPMLifecycleHooks).
func layerKindFixture(t *testing.T, body string, prestart bool) []byte {
	t.Helper()
	scripts := `"start": "node index.js"`
	if prestart {
		scripts += `, "prestart": "true"`
	}
	pkg := `{"name": "faas-layer-kind", "version": "1.0.0", "private": true, "engines": {"node": "22"}, "scripts": {` + scripts + `}, "dependencies": {}}` + "\n"
	index := `require('http').createServer((req, res) => {
  res.writeHead(200, {'content-type': 'text/plain'});
  res.end('` + body + `\n');
}).listen(3000);
`
	return buildTarGz(t, map[string]string{
		"package.json":     pkg,
		"index.js":         index,
		"faas-build-token": body + time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}

func TestSharedRuntimeRollbackMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal shared-runtime rollback test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping metal shared-runtime rollback test")
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", sharedRuntimeBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All)
	defer h.DumpLogs(t)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	const slug = "layerkinds"
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	setAppIdleTimeout(t, h, key, slug, api.IdleTimeoutFloorSeconds)
	store := state.NewPgStore(pool)

	deploy := func(t *testing.T, source []byte, idempotencyKey string) state.Deployment {
		t.Helper()
		raw, status := postMultipartDeployment(t, h, key, slug, source, false, idempotencyKey)
		if status != http.StatusAccepted {
			t.Fatalf("deploy: status=%d body=%s", status, raw)
		}
		depID, _ := parseQueuedDeployment(t, raw)
		ctx, cancel := context.WithTimeout(context.Background(), sourceDeployCtxTimeout())
		defer cancel()
		dep, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling)
		if err != nil {
			t.Fatalf("deployment %s did not go live: %v", depID, err)
		}
		return dep
	}
	park := func(t *testing.T) {
		t.Helper()
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Fatalf("park: status=%d", status)
		}
		if _, err := waitForSourceAppParked(context.Background(), h, appID, 30*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	wake := func(t *testing.T, want, method string) {
		t.Helper()
		body, wakeID, status := doGetWithHostCapturingWakeID(t, h.HTTPClient(), gatewayAppURL(h, slug), slug+".apps.test.example", 90*time.Second)
		if status != http.StatusOK || strings.TrimSpace(string(body)) != want {
			t.Fatalf("wake: status=%d body=%q, want 200 %q", status, body, want)
		}
		if wakeID == "" {
			t.Fatal("gateway response missing x-faas-wake-id header")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, wakeID, method, 10*time.Second); err != nil {
			t.Fatalf("wake %s did not complete via %s: %v", wakeID, method, err)
		}
	}
	// coldThenRestore boots dep without a usable snapshot, then from the
	// snapshot parking that instance takes.
	coldThenRestore := func(t *testing.T, dep state.Deployment, want string) {
		t.Helper()
		ctx := context.Background()
		if _, err := pool.Exec(ctx, `update snapshots set fc_version = 'test-incompatible-' || fc_version where deployment_id = $1 and stale = false`, dep.ID); err != nil {
			t.Fatal(err)
		}
		wake(t, want, "cold_boot")
		if _, err := pool.Exec(ctx, `update snapshots set stale = true where deployment_id = $1 and fc_version like 'test-incompatible-%'`, dep.ID); err != nil {
			t.Fatal(err)
		}
		park(t)
		wake(t, want, "restore")
		park(t)
	}
	rollback := func(t *testing.T, target state.Deployment) {
		t.Helper()
		raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID})
		var accepted api.DeploymentResponse
		if status != http.StatusAccepted || json.Unmarshal(raw, &accepted) != nil {
			t.Fatalf("rollback: status=%d body=%s", status, raw)
		}
		deadline := time.Now().Add(5 * time.Minute)
		for {
			dep, err := store.DeploymentByID(context.Background(), target.ID)
			if err == nil && dep.Status == state.DeployLive && dep.TrafficPercent == 100 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("rollback to %s: status=%s traffic=%d err=%v", target.ID, dep.Status, dep.TrafficPercent, err)
			}
			time.Sleep(2 * time.Second)
		}
	}

	var full, shared state.Deployment
	t.Run("full-layer-deploy", func(t *testing.T) {
		full = deploy(t, layerKindFixture(t, fullLayerBody, true), "aaaaaaaa-0000-4000-8000-000000000001")
		if full.RuntimeBase != "" {
			t.Fatalf("full-layer deployment runtime_base = %q, want the app's own base", full.RuntimeBase)
		}
		park(t)
		wake(t, fullLayerBody, "restore")
		park(t)
	})
	t.Run("shared-runtime-deploy", func(t *testing.T) {
		if full.ID == "" {
			t.Skip("full-layer deployment did not go live")
		}
		shared = deploy(t, layerKindFixture(t, sharedRuntimeBody, false), "aaaaaaaa-0000-4000-8000-000000000002")
		if shared.RuntimeBase != "node22" {
			t.Fatalf("shared deployment runtime_base = %q, want node22", shared.RuntimeBase)
		}
		if full.RootfsBytes > 0 && shared.RootfsBytes*4 > full.RootfsBytes {
			t.Errorf("shared app layer %d bytes is not well below the full layer's %d", shared.RootfsBytes, full.RootfsBytes)
		}
		t.Logf("app layer bytes: full=%d shared=%d", full.RootfsBytes, shared.RootfsBytes)
		park(t)
		wake(t, sharedRuntimeBody, "restore")
		park(t)
	})
	t.Run("rollback-to-full-layer", func(t *testing.T) {
		if shared.ID == "" {
			t.Skip("shared-runtime deployment did not go live")
		}
		rollback(t, full)
		park(t)
		coldThenRestore(t, full, fullLayerBody)
	})
	t.Run("rollback-to-shared-runtime", func(t *testing.T) {
		if shared.ID == "" {
			t.Skip("shared-runtime deployment did not go live")
		}
		rollback(t, shared)
		park(t)
		coldThenRestore(t, shared, sharedRuntimeBody)
	})
}
