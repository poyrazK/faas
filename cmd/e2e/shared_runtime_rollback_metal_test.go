//go:build metal

// shared_runtime_rollback_metal_test.go — ADR-1500 slice 4 acceptance
// (ADR-20261028090000): rollback across Node app bases and layer kinds.
//
// One app gets three deployments:
//   - legacy: Node 22 keeps its own base (base-minimal drive0, the toolchain
//     in drive1), as every deployment before the shared runtime did;
//   - fallback: Node 24 with an npm lifecycle hook is ineligible, so its
//     drive1 keeps the toolchain above the runner-node24 drive0;
//   - shared: Node 24, an /app-only drive1 on the runner-node24 drive0.
//
// Each serves when deployed, and legacy and shared each serve after a
// rollback onto them, from a cold boot and from a snapshot restore: a
// deployment boots the base it recorded, not the app's current one.
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
	legacyBody   = "hello from faas (own base)"
	fallbackBody = "hello from faas (runner fallback)"
	sharedBody   = "hello from faas (shared runtime)"
)

// layerKindFixture is a Node app of the given major answering body. A
// prestart hook makes it ineligible for the /app-only layer
// (sharedruntime.ReasonNPMLifecycleHooks).
func layerKindFixture(t *testing.T, node, body string, prestart bool) []byte {
	t.Helper()
	scripts := `"start": "node index.js"`
	if prestart {
		scripts += `, "prestart": "true"`
	}
	pkg := `{"name": "faas-layer-kind", "version": "1.0.0", "private": true, "engines": {"node": "` + node + `"}, "scripts": {` + scripts + `}, "dependencies": {}}` + "\n"
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", sharedBody)
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

	var legacy, shared state.Deployment
	t.Run("legacy-own-base", func(t *testing.T) {
		legacy = deploy(t, layerKindFixture(t, "22", legacyBody, false), "aaaaaaaa-0000-4000-8000-000000000001")
		if legacy.RuntimeBase != "" {
			t.Fatalf("Node 22 deployment runtime_base = %q, want the app's own base", legacy.RuntimeBase)
		}
		park(t)
		wake(t, legacyBody, "restore")
		park(t)
	})
	t.Run("fallback-on-runner", func(t *testing.T) {
		fallback := deploy(t, layerKindFixture(t, "24", fallbackBody, true), "aaaaaaaa-0000-4000-8000-000000000002")
		if fallback.RuntimeBase != "node24" {
			t.Fatalf("ineligible Node 24 deployment runtime_base = %q, want node24", fallback.RuntimeBase)
		}
		park(t)
		wake(t, fallbackBody, "restore")
		park(t)
	})
	t.Run("shared-runtime", func(t *testing.T) {
		shared = deploy(t, layerKindFixture(t, "24", sharedBody, false), "aaaaaaaa-0000-4000-8000-000000000003")
		if shared.RuntimeBase != "node24" {
			t.Fatalf("shared deployment runtime_base = %q, want node24", shared.RuntimeBase)
		}
		// Every app layer carries guest-init as /sbin/init (rootfs.Build).
		// Beyond that the fixture's /app is a few files, while Railpack's mise
		// Node alone is ~200 MB, so a layer within 8 MiB of guest-init holds
		// only /app.
		guestInit, err := os.Stat(os.Getenv("FAAS_GUEST_INIT"))
		if err != nil {
			t.Fatalf("stat FAAS_GUEST_INIT: %v", err)
		}
		if limit := guestInit.Size() + 8<<20; shared.RootfsBytes <= 0 || shared.RootfsBytes > limit {
			t.Fatalf("app layer = %d bytes, want guest-init (%d) plus an /app-only tree, at most %d", shared.RootfsBytes, guestInit.Size(), limit)
		}
		t.Logf("app layer bytes: own base=%d shared=%d", legacy.RootfsBytes, shared.RootfsBytes)
		park(t)
		wake(t, sharedBody, "restore")
		park(t)
	})
	t.Run("rollback-to-own-base", func(t *testing.T) {
		if legacy.ID == "" || shared.ID == "" {
			t.Skip("an earlier deployment did not go live")
		}
		rollback(t, legacy)
		park(t)
		coldThenRestore(t, legacy, legacyBody)
	})
	t.Run("rollback-to-shared-runtime", func(t *testing.T) {
		if legacy.ID == "" || shared.ID == "" {
			t.Skip("an earlier deployment did not go live")
		}
		rollback(t, shared)
		park(t)
		coldThenRestore(t, shared, sharedBody)
	})
}
