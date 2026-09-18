//go:build metal

package e2e_test

import (
	"context"
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

// TestDirectOCIPort3000Metal proves that direct OCI deployment preserves the
// normal container contract when the image exposes a non-default port. The
// scratch-style process has no port argument: it reads the guest-injected
// PORT, serves on 3000, then survives automatic scale-to-zero and restore.
func TestDirectOCIPort3000Metal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping direct OCI port acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping direct OCI port acceptance")
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "port-3000")
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
		Slug: "oci-port-3000", Type: "app", RequireAuthn: &falsy,
	}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "oci-port-3000")
	setAppIdleTimeout(t, h, key, "oci-port-3000", api.IdleTimeoutFloorSeconds)

	image, ref := e2etest.HelloImageOnPort("library/port-3000", "hello from port 3000", 3000)
	ref = registry.AddImage("library/port-3000", image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/oci-port-3000/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID, _ := parseQueuedDeployment(t, body)

	deployCtx, deployCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer deployCancel()
	dep, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, depID, 75*time.Second)
	if err != nil {
		h.DumpLogs(t)
		t.Fatalf("direct OCI port deployment did not reach live: %v", err)
	}
	if dep.Kind != state.DeploymentKindImage || !dep.FullRootfsAllowAuto || dep.RootfsKey == "" {
		t.Fatalf("deployment policy = kind:%s full_rootfs:%t rootfs_key:%q; want direct full-rootfs image", dep.Kind, dep.FullRootfsAllowAuto, dep.RootfsKey)
	}

	initialCtx, initialCancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer initialCancel()
	if _, err := e2etest.WaitForInstanceState(initialCtx, t, pool, appID, state.StateRunning, 30*time.Second); err != nil {
		t.Fatalf("port-3000 instance did not reach running: %v", err)
	}

	hotBody, hotStatus := doGetWithHost(t, h.HTTPClient(), gatewayAppURL(h, "oci-port-3000"), "oci-port-3000.apps.test.example", 30*time.Second)
	if hotStatus != http.StatusOK || strings.TrimSpace(string(hotBody)) != "hello from port 3000" {
		t.Fatalf("hot request: status=%d body=%q", hotStatus, hotBody)
	}

	zeroCtx, zeroCancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer zeroCancel()
	if err := waitForAppScaleToZero(zeroCtx, pool, appID, 35*time.Second); err != nil {
		t.Fatalf("port-3000 app did not scale to zero automatically: %v", err)
	}

	wakeCtx, wakeCancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer wakeCancel()
	wakeBody, wakeID, wakeStatus := doGetWithHostCapturingWakeID(t, h.HTTPClient(), gatewayAppURL(h, "oci-port-3000"), "oci-port-3000.apps.test.example", 60*time.Second)
	if wakeStatus != http.StatusOK || strings.TrimSpace(string(wakeBody)) != "hello from port 3000" {
		t.Fatalf("scale-from-zero request: status=%d body=%q", wakeStatus, wakeBody)
	}
	if wakeID == "" {
		t.Fatal("scale-from-zero request missing x-faas-wake-id")
	}
	if _, err := e2etest.WaitForWakeMethod(wakeCtx, t, pool, wakeID, "restore", 60*time.Second); err != nil {
		t.Fatalf("port-3000 scale-from-zero request did not restore snapshot: %v", err)
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cleanupCancel()
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/oci-port-3000/park", nil); status != http.StatusAccepted {
		t.Fatalf("cleanup park: status=%d", status)
	}
	if _, err := e2etest.WaitForInstanceState(cleanupCtx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
		t.Fatalf("direct OCI port cleanup park: %v", err)
	}
}
