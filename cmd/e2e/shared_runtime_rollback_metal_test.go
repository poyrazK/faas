//go:build metal

// shared_runtime_rollback_metal_test.go — ADR-1500 slice 4 acceptance
// (ADR-20261029103512): rollback across Node app bases and layer kinds.
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
//
// TestSharedRuntimeRollbackAcrossLayerKindsMetal crosses the other full-layer
// kind: a Dockerfile build on the minimal base (runtime_base empty) and a
// Node 24 source build on the shared runner base (runtime_base node24),
// rolled back in both directions.
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// sharedBaseCeiling bounds a deployment that stages a base in its harness
// (ADR-1500): imaged pulls, builds and scans the Debian parent and the runner
// base before the app layer, about 16 minutes on the n2 metal host. Each
// harness has its own producer records, so every test pays it once. The
// staging reports no deployment progress, so the stall window widens with it.
const (
	sharedBaseCeiling     = e2etest.DefaultBuildCeiling + 25*time.Minute
	sharedBaseStallWindow = 25 * time.Minute
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
	app := newSharedRuntimeApp(t, "layerkinds", sharedBody)
	if app == nil {
		return
	}
	var legacy, shared state.Deployment
	t.Run("legacy-own-base", func(t *testing.T) {
		// The harness's first deployment stages and scans its own base.
		legacy = app.deploy(t, layerKindFixture(t, "22", legacyBody, false), "aaaaaaaa-0000-4000-8000-000000000001", sharedBaseStallWindow, sharedBaseCeiling)
		if legacy.RuntimeBase != "" {
			t.Fatalf("Node 22 deployment runtime_base = %q, want the app's own base", legacy.RuntimeBase)
		}
		app.park(t)
		app.wake(t, legacyBody, "restore")
		app.park(t)
	})
	t.Run("fallback-on-runner", func(t *testing.T) {
		// The first Node 24 deployment stages the runner base.
		fallback := app.deploy(t, layerKindFixture(t, "24", fallbackBody, true), "aaaaaaaa-0000-4000-8000-000000000002", sharedBaseStallWindow, sharedBaseCeiling)
		if fallback.RuntimeBase != "node24" {
			t.Fatalf("ineligible Node 24 deployment runtime_base = %q, want node24", fallback.RuntimeBase)
		}
		app.park(t)
		app.wake(t, fallbackBody, "restore")
		app.park(t)
	})
	t.Run("shared-runtime", func(t *testing.T) {
		shared = app.deploy(t, layerKindFixture(t, "24", sharedBody, false), "aaaaaaaa-0000-4000-8000-000000000003", sharedBaseStallWindow, sharedBaseCeiling)
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
		app.park(t)
		app.wake(t, sharedBody, "restore")
		app.park(t)
	})
	t.Run("rollback-to-own-base", func(t *testing.T) {
		if legacy.ID == "" || shared.ID == "" {
			t.Skip("an earlier deployment did not go live")
		}
		app.rollback(t, legacy)
		app.park(t)
		app.coldThenRestore(t, legacy, legacyBody)
	})
	t.Run("rollback-to-shared-runtime", func(t *testing.T) {
		if legacy.ID == "" || shared.ID == "" {
			t.Skip("an earlier deployment did not go live")
		}
		app.rollback(t, shared)
		app.park(t)
		app.coldThenRestore(t, shared, sharedBody)
	})
}

// sharedRuntimeApp is one metal harness app for the shared-runtime rollback
// tests: deploy a source tarball, park, wake through the gateway, boot from
// cold then restore, and roll back.
type sharedRuntimeApp struct {
	h     *e2etest.Harness
	pool  *pgxpool.Pool
	store *state.PgStore
	key   string
	slug  string
	appID string
}

// newSharedRuntimeApp starts a metal harness with one Hobby app, or returns
// nil when the host has no migrated Postgres. deployBody is what the stub
// deploy base answers on a host without real bases.
func newSharedRuntimeApp(t *testing.T, slug, deployBody string) *sharedRuntimeApp {
	t.Helper()
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal shared-runtime test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping metal shared-runtime test")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return nil
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", deployBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All)
	t.Cleanup(func() { h.DumpLogs(t) })
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	app := &sharedRuntimeApp{h: h, pool: pool, store: state.NewPgStore(pool), key: key, slug: slug, appID: mustGetAppID(t, h, key, slug)}
	setAppIdleTimeout(t, h, key, slug, api.IdleTimeoutFloorSeconds)
	return app
}

func (a *sharedRuntimeApp) deploy(t *testing.T, source []byte, idempotencyKey string, stall, ceiling time.Duration) state.Deployment {
	t.Helper()
	raw, status := postMultipartDeployment(t, a.h, a.key, a.slug, source, false, idempotencyKey)
	if status != http.StatusAccepted {
		t.Fatalf("deploy: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)
	ctx, cancel := context.WithTimeout(context.Background(), ceiling+5*time.Minute)
	defer cancel()
	dep, _, err := e2etest.WaitForSourceDeployment(ctx, t, a.pool, depID, stall, ceiling)
	if err != nil {
		t.Fatalf("deployment %s did not go live: %v", depID, err)
	}
	return dep
}

func (a *sharedRuntimeApp) park(t *testing.T) {
	t.Helper()
	// A capture that outlasts apid's 5 s drain wait (6 s for 512 MB on
	// n2) answers a retryable 503, as in TestSourceDeployWakeMetal.
	raw, status := doReq(t, a.h, a.key, http.MethodPost, "/v1/apps/"+a.slug+"/park", nil)
	for attempt := 0; attempt < 3 && status == http.StatusServiceUnavailable; attempt++ {
		t.Logf("park: status=%d body=%s; retrying", status, raw)
		time.Sleep(5 * time.Second)
		raw, status = doReq(t, a.h, a.key, http.MethodPost, "/v1/apps/"+a.slug+"/park", nil)
	}
	if status != http.StatusNoContent {
		t.Fatalf("park: status=%d body=%s", status, raw)
	}
	if _, err := waitForSourceAppParked(context.Background(), a.h, a.appID, 30*time.Second); err != nil {
		t.Fatal(err)
	}
}

func (a *sharedRuntimeApp) wake(t *testing.T, want, method string) {
	t.Helper()
	// A 503 asks the client to retry (a routing read that overran its
	// budget on a loaded host); clients do, so the test does too.
	var body []byte
	var wakeID string
	var status int
	for attempt := 0; attempt < 4; attempt++ {
		body, wakeID, status = doGetWithHostCapturingWakeID(t, a.h.HTTPClient(), gatewayAppURL(a.h, a.slug), a.slug+".apps.test.example", 90*time.Second)
		if status != http.StatusServiceUnavailable {
			break
		}
		t.Logf("wake attempt %d: 503 %s", attempt+1, body)
		time.Sleep(5 * time.Second)
	}
	if status != http.StatusOK || strings.TrimSpace(string(body)) != want {
		t.Fatalf("wake: status=%d body=%q, want 200 %q", status, body, want)
	}
	if wakeID == "" {
		t.Fatal("gateway response missing x-faas-wake-id header")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForWakeMethod(ctx, t, a.pool, wakeID, method, 10*time.Second); err != nil {
		t.Fatalf("wake %s did not complete via %s: %v", wakeID, method, err)
	}
}

// coldThenRestore boots dep without a usable snapshot, then from the
// snapshot parking that instance takes.
func (a *sharedRuntimeApp) coldThenRestore(t *testing.T, dep state.Deployment, want string) {
	t.Helper()
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, `update snapshots set fc_version = 'test-incompatible-' || fc_version where deployment_id = $1 and stale = false`, dep.ID); err != nil {
		t.Fatal(err)
	}
	a.wake(t, want, "cold_boot")
	if _, err := a.pool.Exec(ctx, `update snapshots set stale = true where deployment_id = $1 and fc_version like 'test-incompatible-%'`, dep.ID); err != nil {
		t.Fatal(err)
	}
	a.park(t)
	a.wake(t, want, "restore")
	a.park(t)
}

func (a *sharedRuntimeApp) rollback(t *testing.T, target state.Deployment) {
	t.Helper()
	raw, status := doReq(t, a.h, a.key, http.MethodPost, "/v1/apps/"+a.slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID})
	var accepted api.DeploymentResponse
	if status != http.StatusAccepted || json.Unmarshal(raw, &accepted) != nil {
		t.Fatalf("rollback: status=%d body=%s", status, raw)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		dep, err := a.store.DeploymentByID(context.Background(), target.ID)
		if err == nil && dep.Status == state.DeployLive && dep.TrafficPercent == 100 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("rollback to %s: status=%s traffic=%d err=%v", target.ID, dep.Status, dep.TrafficPercent, err)
		}
		time.Sleep(2 * time.Second)
	}
}

const (
	sharedRollbackSlug       = "rtrollback"
	sharedRollbackFullBody   = "hello from faas (dockerfile fixture)"
	sharedRollbackSharedBody = sourceDeployHelloBody
)

func TestSharedRuntimeRollbackAcrossLayerKindsMetal(t *testing.T) {
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
	// Same builder and deploy bases as TestSourceDeployWakeMetal.
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	builderBaseRef := registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", sourceDeployHelloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideBuilderBase(t, builderBaseRef)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All, "FAAS_SOURCE_APP_SHARED_RUNTIME=1")
	defer h.DumpLogs(t)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: sharedRollbackSlug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, sharedRollbackSlug)
	t.Cleanup(func() {
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+sharedRollbackSlug+"/park", nil); status != http.StatusNoContent {
			t.Errorf("cleanup park: status=%d", status)
		}
	})

	full := sharedRollbackDeploy(t, h, pool, key, DockerfileFixture(t), true)
	if full.RuntimeBase != "" {
		t.Fatalf("Dockerfile deployment runtime_base = %q, want the minimal base", full.RuntimeBase)
	}
	// Node 24: the Node 22 runner is musl and never shared (ADR-20261029103512).
	shared := sharedRollbackDeploy(t, h, pool, key, layerKindFixture(t, "24", sharedRollbackSharedBody, false), false)
	if shared.RuntimeBase != "node24" {
		t.Fatalf("Node deployment runtime_base = %q, want node24 (the shared runner base)", shared.RuntimeBase)
	}
	sharedRollbackServes(t, h, key, appID, sharedRollbackSharedBody)

	// Body-less rollback targets the newest superseded deployment, so two
	// rollbacks cross the layer kinds in both directions.
	for _, step := range []struct {
		name, wantID, wantBody string
	}{
		{"shared-to-full", full.ID, sharedRollbackFullBody},
		{"full-to-shared", shared.ID, sharedRollbackSharedBody},
	} {
		t.Run(step.name, func(t *testing.T) {
			raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+sharedRollbackSlug+"/rollback", nil)
			if status != http.StatusAccepted {
				t.Fatalf("rollback: status=%d body=%s", status, raw)
			}
			var out api.DeploymentResponse
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("decode rollback: %v body=%s", err, raw)
			}
			if out.ID != step.wantID {
				t.Fatalf("rollback target = %s, want %s", out.ID, step.wantID)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, out.ID, 3*time.Minute); err != nil {
				t.Fatalf("rolled-back deployment did not reach live: %v", err)
			}
			sharedRollbackServes(t, h, key, appID, step.wantBody)
		})
	}
}

// sharedRollbackDeploy uploads source and waits for its deployment to go live.
// Each deployment may stage its base in this harness, so it gets the staging
// bounds.
func sharedRollbackDeploy(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key string, source []byte, dockerfile bool) state.Deployment {
	t.Helper()
	raw, status := postMultipartDeployment(t, h, key, sharedRollbackSlug, source, dockerfile, "")
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)
	ctx, cancel := context.WithTimeout(context.Background(), sharedBaseCeiling+5*time.Minute)
	defer cancel()
	dep, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, sharedBaseStallWindow, sharedBaseCeiling)
	if err != nil {
		t.Fatalf("deployment %s did not reach live: %v", depID, err)
	}
	return dep
}

// sharedRollbackServes parks the app, then wakes it with one request and
// checks the live deployment answered.
func sharedRollbackServes(t *testing.T, h *e2etest.Harness, key, appID, wantBody string) {
	t.Helper()
	// A capture that outlasts apid's drain wait answers a retryable 503.
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+sharedRollbackSlug+"/park", nil)
	for attempt := 0; attempt < 3 && status == http.StatusServiceUnavailable; attempt++ {
		t.Logf("park: status=%d body=%s; retrying", status, raw)
		time.Sleep(5 * time.Second)
		raw, status = doReq(t, h, key, http.MethodPost, "/v1/apps/"+sharedRollbackSlug+"/park", nil)
	}
	if status != http.StatusNoContent {
		t.Fatalf("park: status=%d body=%s", status, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := waitForSourceAppParked(ctx, h, appID, 55*time.Second); err != nil {
		t.Fatalf("app did not park: %v", err)
	}
	host := sharedRollbackSlug + ".apps.test.example"
	body, wakeID, status := doGetWithHostCapturingWakeID(t, h.HTTPClient(), gatewayAppURL(h, sharedRollbackSlug), host, 60*time.Second)
	if status != http.StatusOK {
		t.Fatalf("wake request: status=%d body=%s", status, body)
	}
	if got := strings.TrimSpace(string(body)); got != wantBody {
		t.Fatalf("wake %s served %q, want %q", wakeID, got, wantBody)
	}
}
