//go:build metal

package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDirectOCIAutoscaleScaleToZeroMetal proves the Cloud Run-shaped
// lifecycle for an arbitrary OCI image: traffic can admit a second instance,
// the idle reaper returns the app to zero without an explicit park call, and
// the next request restores the parked snapshot.
func TestDirectOCIAutoscaleScaleToZeroMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping direct OCI autoscale acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping direct OCI autoscale acceptance")
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
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: "oci-autoscale", Type: "app", MaxConcurrency: 2, RequireAuthn: &falsy,
	}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "oci-autoscale")
	setAppIdleTimeout(t, h, key, "oci-autoscale", api.IdleTimeoutFloorSeconds)

	image, ref := e2etest.HelloImage("library/autoscale", "hello from autoscaled oci")
	ref = registry.AddImage("library/autoscale", image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/oci-autoscale/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID, _ := parseQueuedDeployment(t, body)
	deployCtx, deployCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer deployCancel()
	dep, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, depID, 75*time.Second)
	if err != nil {
		h.DumpLogs(t)
		t.Fatalf("direct OCI deployment did not reach live: %v", err)
	}
	if dep.Kind != state.DeploymentKindImage || !dep.FullRootfsAllowAuto || dep.RootfsKey == "" {
		t.Fatalf("deployment policy = kind:%s full_rootfs:%t rootfs_key:%q; want direct full-rootfs image", dep.Kind, dep.FullRootfsAllowAuto, dep.RootfsKey)
	}

	rpsTarget := 1
	patchBody := api.UpdateAppRequest{AutoscaleTargetRPS: &rpsTarget}
	if raw, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/oci-autoscale", patchBody); status != http.StatusOK {
		t.Fatalf("enable RPS autoscale: status=%d body=%s", status, raw)
	}

	initialCtx, initialCancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer initialCancel()
	if _, err := e2etest.WaitForInstanceState(initialCtx, t, pool, appID, state.StateRunning, 30*time.Second); err != nil {
		t.Fatalf("initial OCI instance did not reach running: %v", err)
	}

	// Keep the request path real (gatewayd → vmmd → guest) while making the
	// one-second RPS sampler observe an unambiguous burst above the target.
	const burst = 24
	results := make(chan error, burst)
	var wg sync.WaitGroup
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- directOCIGatewayRequest(h.HTTPClient(), gatewayAppURL(h, "oci-autoscale"), "oci-autoscale.apps.test.example")
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("autoscale traffic burst: %v", err)
		}
	}

	scaleCtx, scaleCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer scaleCancel()
	if _, err := waitForRunningInstanceCount(scaleCtx, pool, appID, 2, 40*time.Second); err != nil {
		t.Fatalf("OCI traffic did not scale out to two instances: %v", err)
	}

	zeroCtx, zeroCancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer zeroCancel()
	if err := waitForAppScaleToZero(zeroCtx, pool, appID, 35*time.Second); err != nil {
		t.Fatalf("idle OCI app did not scale to zero automatically: %v", err)
	}

	wakeCtx, wakeCancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer wakeCancel()
	wakeBody, wakeID, status := doGetWithHostCapturingWakeID(t, h.HTTPClient(), gatewayAppURL(h, "oci-autoscale"), "oci-autoscale.apps.test.example", 60*time.Second)
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		t.Fatalf("scale-from-zero request: status=%d body=%s", status, wakeBody)
	}
	if got := strings.TrimSpace(string(wakeBody)); got != "hello from autoscaled oci" {
		t.Fatalf("scale-from-zero body=%q, want arbitrary OCI response", got)
	}
	if wakeID == "" {
		t.Fatal("scale-from-zero request missing x-faas-wake-id")
	}
	if _, err := e2etest.WaitForWakeMethod(wakeCtx, t, pool, wakeID, "restore", 60*time.Second); err != nil {
		t.Fatalf("scale-from-zero request did not restore snapshot: %v", err)
	}
}

func directOCIGatewayRequest(client *http.Client, url, host string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Host = host
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("status=%d", resp.StatusCode)
	}
	return nil
}

func waitForRunningInstanceCount(ctx context.Context, pool *pgxpool.Pool, appID string, want int, deadline time.Duration) ([]state.Instance, error) {
	store := state.NewPgStore(pool)
	end := time.Now().Add(deadline)
	for {
		instances, err := store.ListInstancesForApp(ctx, appID)
		if err != nil {
			return nil, err
		}
		running := 0
		for _, instance := range instances {
			if state.State(instance.State) == state.StateRunning {
				running++
			}
		}
		if running >= want {
			return instances, nil
		}
		if !time.Now().Before(end) {
			return instances, fmt.Errorf("saw %d running instances, want %d", running, want)
		}
		select {
		case <-ctx.Done():
			return instances, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func waitForAppScaleToZero(ctx context.Context, pool *pgxpool.Pool, appID string, deadline time.Duration) error {
	store := state.NewPgStore(pool)
	end := time.Now().Add(deadline)
	for {
		instances, err := store.ListInstancesForApp(ctx, appID)
		if err != nil {
			return err
		}
		parked := false
		live := false
		for _, instance := range instances {
			if state.State(instance.State) == state.StateParked {
				parked = true
			}
			if state.IsLive(instance.State) {
				live = true
			}
		}
		if parked && !live {
			return nil
		}
		if !time.Now().Before(end) {
			return fmt.Errorf("instances did not settle at zero: parked=%t live=%t", parked, live)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
