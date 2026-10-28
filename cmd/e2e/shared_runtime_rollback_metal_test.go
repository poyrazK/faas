//go:build metal

package e2e_test

// shared_runtime_rollback_metal_test.go — ADR-1500 slice 4 acceptance:
// rollback across the two source layer kinds. A Dockerfile build keeps the
// full layer on the minimal base (runtime_base empty); a Node source build on
// a node with FAAS_SOURCE_APP_SHARED_RUNTIME=1 boots the shared runner base
// with an /app-only layer (runtime_base node22). Rolling back either way must
// boot the older deployment on its own base.

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
	shared := sharedRollbackDeploy(t, h, pool, key, NodeFixture(t), false)
	if shared.RuntimeBase != "node22" {
		t.Fatalf("Node deployment runtime_base = %q, want node22 (the shared runner base)", shared.RuntimeBase)
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
func sharedRollbackDeploy(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key string, source []byte, dockerfile bool) state.Deployment {
	t.Helper()
	raw, status := postMultipartDeployment(t, h, key, sharedRollbackSlug, source, dockerfile, "")
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)
	ctx, cancel := context.WithTimeout(context.Background(), sourceDeployCtxTimeout())
	defer cancel()
	dep, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling)
	if err != nil {
		t.Fatalf("deployment %s did not reach live: %v", depID, err)
	}
	return dep
}

// sharedRollbackServes parks the app, then wakes it with one request and
// checks the live deployment answered.
func sharedRollbackServes(t *testing.T, h *e2etest.Harness, key, appID, wantBody string) {
	t.Helper()
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+sharedRollbackSlug+"/park", nil); status != http.StatusNoContent {
		t.Fatalf("park: status=%d", status)
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
