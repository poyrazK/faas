//go:build metal

// adr: 1151 — the frozen tier through the real daemons: schedd freezes an
// idle function or app instance through vmmd instead of parking it, its
// billing interval closes while it is frozen, the next request through the
// gateway thaws the same VM, and deleting the app retires a frozen VM.
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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFrozenTierMetal(t *testing.T) { runFrozenTierMetal(t, false) }

// TestFrozenTierAppMetal freezes a request-mode app, which needs schedd's
// frozen_tier_apps switch as well (ADR-1151 step 3).
func TestFrozenTierAppMetal(t *testing.T) { runFrozenTierMetal(t, true) }

func runFrozenTierMetal(t *testing.T, appsTier bool) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal frozen tier test")
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
	builderBaseRef := registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideBuilderBase(t, builderBaseRef)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	defer h.DumpLogs(t)
	// The frozen tier is off by default: turn it on for this schedd.
	if err := h.SetScheddEnv("FAAS_FROZEN_TIER", "1"); err != nil {
		t.Fatal(err)
	}
	if appsTier {
		if err := h.SetScheddEnv("FAAS_FROZEN_TIER_APPS", "1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.RestartSchedd(); err != nil {
		t.Fatalf("restart schedd with the frozen tier on: %v", err)
	}
	key := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	create := api.CreateAppRequest{Slug: "frozen", Type: "app", RequireAuthn: &falsy}
	if appsTier {
		// The thaw must run the app's after_restore hook and confirm it, or
		// schedd parks the frozen VM and the request below lands on another
		// instance. The fixture server answers POST / with 200.
		create.AfterRestore = &api.AfterRestoreHook{Path: "/"}
	}
	if got := postOK(t, h, key, "/v1/apps", create); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "frozen")
	// Neither daemon destroys VMs when it stops, so a failure part-way would
	// leave the app's VM running. Retire the app first (cleanups run before
	// the harness stops the daemons).
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		_, _ = doReq(t, h, key, http.MethodDelete, "/v1/apps/frozen", nil)
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			ins, err := state.NewPgStore(pool).ListInstancesForApp(context.Background(), appID)
			if err != nil {
				return
			}
			resident := false
			for _, in := range ins {
				resident = resident || state.State(in.State).CountsForRAM()
			}
			if !resident {
				return
			}
			time.Sleep(time.Second)
		}
	})
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/frozen/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	// Imaging (layer build and scans) takes minutes on a busy shared test node.
	if _, err := e2etest.WaitForDeploymentLive(context.Background(), t, pool, dep.ID, 15*time.Minute); err != nil {
		t.Fatalf("deployment did not go live: %v", err)
	}
	// Without the apps switch only functions are frozen. A function deploy
	// needs a function runner image built in a builder VM; this test deploys
	// the prebuilt hello image as an app and then marks the app a function,
	// which is all the frozen tier reads. The runtime stays empty: a runtime
	// would make wakes boot that runner's base drive instead of the deployed
	// image's. With the apps switch the app freezes as an app.
	if !appsTier {
		if _, err := pool.Exec(context.Background(),
			`update apps set type = 'function' where id = $1`, appID); err != nil {
			t.Fatalf("mark app a function: %v", err)
		}
	}
	setAppIdleTimeout(t, h, key, "frozen", api.IdleTimeoutFloorSeconds)

	client := h.HTTPClient()
	url := gatewayAppURL(h, "frozen")
	host := "frozen.apps.test.example"
	if err := e2etest.WaitForHTTPReady(context.Background(), t, client, url, 5*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}

	// Serve, go idle, and wait for the reaper to freeze the instance. Freezing
	// needs the deployment's snapshot, so an idle period that parks instead
	// (no snapshot yet) is followed by another request.
	var frozen state.Instance
	deadline := time.Now().Add(4 * time.Minute)
	for frozen.ID == "" {
		if time.Now().After(deadline) {
			t.Fatal("no instance was frozen within 4 minutes")
		}
		getFrozenApp(t, client, url, host, key)
		ins, err := e2etest.WaitForInstanceState(context.Background(), t, pool, appID, state.StateFrozen, 45*time.Second)
		if err != nil {
			t.Logf("not frozen yet (states %s); serving again", instanceStates(ins))
			continue
		}
		frozen = pickState(ins, state.StateFrozen)
	}
	t.Logf("frozen: instance %s on node %s", frozen.ID, frozen.NodeID)
	assertBillingOpen(t, pool, frozen.ID, false)

	// The next request thaws the same VM.
	started := time.Now()
	wake := getFrozenApp(t, client, url, host, key)
	thawed := time.Since(started)
	ins, err := e2etest.WaitForInstanceState(context.Background(), t, pool, appID, state.StateRunning, 10*time.Second)
	if err != nil {
		t.Fatalf("no running instance after the thaw: %v", err)
	}
	if running := pickState(ins, state.StateRunning); running.ID != frozen.ID {
		t.Fatalf("request ran on %s, want the thawed frozen instance %s", running.ID, frozen.ID)
	}
	if wake != "restored" {
		t.Errorf("x-faas-wake = %q after a thaw, want restored", wake)
	}
	assertBillingOpen(t, pool, frozen.ID, true)
	t.Logf("thaw through the gateway: request answered in %s (x-faas-wake=%s)", thawed, wake)

	// Freeze it again, then delete the app: the frozen VM must be retired.
	if _, err := e2etest.WaitForInstanceState(context.Background(), t, pool, appID, state.StateFrozen, 60*time.Second); err != nil {
		t.Fatalf("instance did not freeze a second time: %v", err)
	}
	if raw, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/frozen", nil); status >= 300 {
		t.Fatalf("delete app: status=%d body=%s", status, raw)
	}
	retireDeadline := time.Now().Add(2 * time.Minute)
	for {
		ins, err := state.NewPgStore(pool).ListInstancesForApp(context.Background(), appID)
		if err != nil {
			t.Fatal(err)
		}
		live := false
		for _, in := range ins {
			live = live || state.State(in.State).CountsForRAM()
		}
		if !live {
			break
		}
		if time.Now().After(retireDeadline) {
			t.Fatalf("app deleted but instances still resident: %s", instanceStates(ins))
		}
		time.Sleep(time.Second)
	}
}

// getFrozenApp sends one request through the gateway and returns the
// x-faas-wake tier it reports.
func getFrozenApp(t *testing.T, client *http.Client, url, host, key string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	// Pro apps default to bearer auth for public requests.
	req.Header.Set("Authorization", "Bearer "+key)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
		t.Fatalf("GET %s: status %d body %q", url, resp.StatusCode, body)
	}
	return resp.Header.Get("x-faas-wake")
}

func pickState(instances []state.Instance, want state.State) state.Instance {
	for _, in := range instances {
		if state.State(in.State) == want {
			return in
		}
	}
	return state.Instance{}
}

func instanceStates(instances []state.Instance) string {
	parts := make([]string, 0, len(instances))
	for _, in := range instances {
		parts = append(parts, in.ID[:8]+"="+in.State)
	}
	return strings.Join(parts, " ")
}

// assertBillingOpen checks whether the instance has an open billing
// interval: none while frozen (ADR-1151), one again after the thaw.
func assertBillingOpen(t *testing.T, pool *pgxpool.Pool, instanceID string, want bool) {
	t.Helper()
	var open int
	if err := pool.QueryRow(context.Background(),
		`select count(*) from instance_billing_intervals where instance_id = $1 and ended_at is null`, instanceID).Scan(&open); err != nil {
		t.Fatalf("read billing intervals: %v", err)
	}
	if (open > 0) != want {
		t.Fatalf("instance %s open billing intervals = %d, want open=%v", instanceID, open, want)
	}
}
