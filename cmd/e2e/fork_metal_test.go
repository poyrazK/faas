//go:build metal

// fork_metal_test.go — ADR-732 production forks, end to end on real
// Firecracker: apid admits the fork, schedd's ForkCoordinator restores the
// app's capture into a quarantined mode='fork' instance, the gateway routes
// a token-bearing request to it (and never routes ordinary traffic there),
// and the coordinator destroys it on cancel and at its TTL.
//
// Requires /dev/kvm, root, Firecracker on PATH and FAAS_TEST_KERNEL, like
// deploy_wake_metal_test.go.
package e2e_test

// adr: 732

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestForkMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal fork test")
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

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_APP_FORKS=1")
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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

	var fork api.AppForkResponse
	t.Run("create-and-restore", func(t *testing.T) {
		// Keep the create response: only it carries the one-time token.
		fork = createForkE2E(t, h, key, 600)
		waitForkStatus(ctx, t, h, key, fork.ID, "running", 120*time.Second)
		ins := forkInstance(ctx, t, pool, appID)
		if ins.State != string(state.StateRunning) || ins.DeploymentID != dep.ID {
			t.Fatalf("fork instance = %+v, want running on the live deployment", ins)
		}
	})

	t.Run("quarantine-chains-only-on-the-fork", func(t *testing.T) {
		rows, err := state.NewPgStore(pool).ListInstancesForApp(ctx, appID)
		if err != nil {
			t.Fatal(err)
		}
		for _, ins := range rows {
			if ins.State != string(state.StateRunning) || ins.Netns == "" {
				continue
			}
			out, _ := exec.Command("ip", "netns", "exec", ins.Netns, "nft", "list", "chain", "ip", "faas", "quarantine_forward").CombinedOutput()
			has := strings.Contains(string(out), "quarantine_drop")
			if has != state.IsFork(ins.Mode) {
				t.Errorf("instance %s mode=%s has quarantine chain=%v:\n%s", ins.ID, ins.Mode, has, out)
			}
		}
	})

	t.Run("token-routes-to-the-fork", func(t *testing.T) {
		headers, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/", nil,
			map[string]string{api.ForkHeader: fork.ID, api.ForkTokenHeader: fork.AccessToken})
		if status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody || headers.Get("X-Gregale-Fork-Served") != "1" {
			t.Fatalf("fork request = %d %q served=%q", status, body, headers.Get("X-Gregale-Fork-Served"))
		}
		_, _, status = doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/", nil,
			map[string]string{api.ForkHeader: fork.ID, api.ForkTokenHeader: "gfk_wrong"})
		if status != http.StatusNotFound {
			t.Fatalf("wrong token = %d, want 404", status)
		}
	})

	t.Run("ordinary-traffic-never-reaches-the-fork", func(t *testing.T) {
		for range 20 {
			headers, _, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/", nil)
			if status != http.StatusOK || headers.Get("X-Gregale-Fork-Served") != "" {
				t.Fatalf("ordinary request = %d served-by-fork=%q", status, headers.Get("X-Gregale-Fork-Served"))
			}
		}
	})

	t.Run("cancel-destroys", func(t *testing.T) {
		instanceID := forkInstance(ctx, t, pool, appID).ID
		if _, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/hello/forks/"+fork.ID, nil); status != http.StatusAccepted {
			t.Fatalf("cancel = %d", status)
		}
		waitForkStatus(ctx, t, h, key, fork.ID, "cancelled", 60*time.Second)
		waitInstanceStopped(ctx, t, pool, instanceID, 60*time.Second)
	})

	t.Run("ttl-destroys", func(t *testing.T) {
		short := createForkE2E(t, h, key, 60)
		waitForkStatus(ctx, t, h, key, short.ID, "running", 120*time.Second)
		instanceID := forkInstance(ctx, t, pool, appID).ID
		waitForkStatus(ctx, t, h, key, short.ID, "expired", 120*time.Second)
		waitInstanceStopped(ctx, t, pool, instanceID, 60*time.Second)
	})
}

func createForkE2E(t *testing.T, h *e2etest.Harness, key string, ttl int) api.AppForkResponse {
	t.Helper()
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/forks", api.CreateAppForkRequest{TTLSeconds: &ttl})
	if status != http.StatusAccepted {
		t.Fatalf("create fork = %d %s", status, raw)
	}
	var out api.AppForkResponse
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		t.Fatalf("decode fork: %v %s", err, raw)
	}
	return out
}

func waitForkStatus(ctx context.Context, t *testing.T, h *e2etest.Harness, key, id, want string, timeout time.Duration) api.AppForkResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last api.AppForkResponse
	for time.Now().Before(deadline) && ctx.Err() == nil {
		raw, status := doReq(t, h, key, http.MethodGet, "/v1/apps/hello/forks/"+id, nil)
		if status == http.StatusOK && json.Unmarshal(raw, &last) == nil {
			if string(last.Status) == want {
				return last
			}
			if last.Failure != nil {
				t.Fatalf("fork %s failed: %s %s", id, last.Failure.Code, last.Failure.Message)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("fork %s status = %q after %s, want %q", id, last.Status, timeout, want)
	return last
}

func forkInstance(ctx context.Context, t *testing.T, pool *pgxpool.Pool, appID string) state.Instance {
	t.Helper()
	rows, err := state.NewPgStore(pool).ListInstancesForApp(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range rows {
		if state.IsFork(ins.Mode) && ins.State == string(state.StateRunning) {
			return ins
		}
	}
	t.Fatalf("no running fork instance for app %s", appID)
	return state.Instance{}
}

func waitInstanceStopped(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		ins, err := state.NewPgStore(pool).InstanceByID(ctx, id)
		if err == nil && (ins.State == string(state.StateStopped) || ins.State == string(state.StateFailed)) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("fork instance %s not destroyed within %s", id, timeout)
}
