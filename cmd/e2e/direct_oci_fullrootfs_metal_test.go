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

// TestDirectOCIFullRootfsMetal proves the direct-container portability path:
// a digest-pinned image that is not built from a Gregale runtime base still
// reaches Live, parks to zero, and wakes through the snapshot path. The
// single-layer HelloImage deliberately has no shared-base prefix, so a green
// deployment proves dispatchFullRootfs rather than the two-drive path.
func TestDirectOCIFullRootfsMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping direct OCI full-rootfs acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping direct OCI full-rootfs acceptance")
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "full-rootfs")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideBuilderBase(t, builderBaseRef)
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
		Slug:         "oci-fullrootfs",
		Type:         "app",
		RequireAuthn: &falsy,
	}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "oci-fullrootfs")
	setAppIdleTimeout(t, h, key, "oci-fullrootfs", api.IdleTimeoutFloorSeconds)

	image, ref := e2etest.HelloImage("library/full-rootfs", "hello from arbitrary oci")
	ref = registry.AddImage("library/full-rootfs", image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/oci-fullrootfs/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID, _ := parseQueuedDeployment(t, body)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dep, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 75*time.Second)
	if err != nil {
		h.DumpLogs(t)
		t.Fatalf("direct OCI deployment did not reach live: %v", err)
	}
	if dep.Kind != state.DeploymentKindImage || dep.BuildID != "" {
		t.Fatalf("deployment kind/build = (%s, %q), want direct image with no source build", dep.Kind, dep.BuildID)
	}
	if !dep.FullRootfsAllowAuto || dep.FullRootfsOverride != nil {
		t.Fatalf("full-rootfs policy = auto:%t override:%v, want paid-plan auto with no override", dep.FullRootfsAllowAuto, dep.FullRootfsOverride)
	}
	if dep.RootfsKey == "" || dep.RootfsBytes <= 0 {
		t.Fatalf("full-rootfs artifact = key:%q bytes:%d, want non-empty artifact", dep.RootfsKey, dep.RootfsBytes)
	}

	parkCtx, parkCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer parkCancel()
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/oci-fullrootfs/park", nil); status != http.StatusAccepted {
		t.Fatalf("park: status=%d", status)
	}
	if _, err := e2etest.WaitForInstanceState(parkCtx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
		t.Fatalf("direct OCI deployment did not park: %v", err)
	}

	wakeCtx, wakeCancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer wakeCancel()
	requestBody, wakeID, status := doGetWithHostCapturingWakeID(t, h.HTTPClient(), gatewayAppURL(h, "oci-fullrootfs"), "oci-fullrootfs.apps.test.example", 60*time.Second)
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		t.Fatalf("wake request: status=%d body=%s", status, requestBody)
	}
	if got := strings.TrimSpace(string(requestBody)); got != "hello from arbitrary oci" {
		t.Fatalf("wake body=%q, want arbitrary OCI response", got)
	}
	if wakeID == "" {
		t.Fatal("wake request missing x-faas-wake-id")
	}
	if _, err := e2etest.WaitForWakeMethod(wakeCtx, t, pool, wakeID, "restore", 60*time.Second); err != nil {
		t.Fatalf("direct OCI wake did not restore from snapshot: %v", err)
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cleanupCancel()
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/oci-fullrootfs/park", nil); status != http.StatusAccepted {
		t.Fatalf("cleanup park: status=%d", status)
	}
	if _, err := e2etest.WaitForInstanceState(cleanupCtx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
		t.Fatalf("direct OCI cleanup park: %v", err)
	}
}
