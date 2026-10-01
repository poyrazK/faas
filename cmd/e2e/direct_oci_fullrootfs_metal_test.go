//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
	testDirectOCIFullRootfs(t, directOCIContractOptions{})
}

// The process contract must survive image preparation and snapshot restore,
// rather than merely appearing in projected manifest metadata.
func TestDirectOCIProcessContractMetal(t *testing.T) {
	testDirectOCIFullRootfs(t, directOCIContractOptions{Process: true})
}

func TestDirectOCIMainHealthcheckMetal(t *testing.T) {
	testDirectOCIFullRootfs(t, directOCIContractOptions{Process: true, Healthcheck: true})
}

func TestDirectOCIMainHealthcheckWithCompanionMetal(t *testing.T) {
	testDirectOCIFullRootfs(t, directOCIContractOptions{Process: true, Healthcheck: true, Companion: true})
}

type directOCIContractOptions struct {
	Process, Healthcheck, Companion bool
}

func testDirectOCIFullRootfs(t *testing.T, options directOCIContractOptions) {
	t.Helper()
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

	var extraEnv []string
	if options.Companion {
		// Exercise the production seal/unseal path with a test-owned identity.
		keyDir := t.TempDir()
		keyPath, pubPath := filepath.Join(keyDir, "host.age"), filepath.Join(keyDir, "host.age.pub")
		preSeedHostKey(t, keyPath, pubPath)
		extraEnv = []string{"FAAS_HOST_KEY_PATH=" + keyPath, "FAAS_HOST_AGE_RECIPIENT_PATH=" + pubPath}
	}
	h := e2etest.Start(t, pool, e2etest.DeployWake, extraEnv...)
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

	// This image deliberately has no /healthz endpoint. A direct OCI image's
	// normal-container contract is TCP readiness unless the customer supplies
	// an explicit health path override.
	image, _ := e2etest.HelloImageWithoutHealthz("library/full-rootfs", "hello from arbitrary oci")
	if options.Process {
		image, _ = e2etest.HelloImageWithProcessContract("library/full-rootfs", "hello from arbitrary oci", "1001:2001")
	}
	if options.Healthcheck {
		image, _ = e2etest.HelloImageWithProcessHealthcheck("library/full-rootfs", "hello from arbitrary oci", "1001:2001")
	}
	ref := registry.AddImage("library/full-rootfs", image)
	request := api.CreateDeploymentRequest{Image: ref}
	if options.Healthcheck {
		request.Overrides = &api.CreateDeploymentOverrides{Env: map[string]string{"DEPLOYMENT_MARKER": "runtime-deployment"}}
	}
	if options.Companion {
		companion, _ := e2etest.HelloImageOnPort("library/health-companion", "companion", 9091)
		companionRef := registry.AddImage("library/health-companion", companion)
		request.Companions = api.Companions{{Name: "helper", Type: api.SidecarTypeSidecar, Image: companionRef, Port: 9091, RamMB: 64}}
	}
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/oci-fullrootfs/deployments", request)
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID := parseImageDeployment(t, body)

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

	type evidence struct {
		UID              int    `json:"uid"`
		GID              int    `json:"gid"`
		WorkingDir       string `json:"working_dir"`
		Marker           string `json:"marker"`
		DeploymentMarker string `json:"deployment_marker"`
		Cgroup           string `json:"cgroup"`
		Count            int    `json:"count"`
	}
	checkProcess := func(minProbeCount int) int {
		t.Helper()
		if !options.Process {
			return 0
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			body, status := doGetWithHost(t, h.HTTPClient(), gatewayAppURL(h, "oci-fullrootfs")+"contract", "oci-fullrootfs.apps.test.example", 30*time.Second)
			if status != http.StatusOK {
				t.Fatalf("process contract status=%d body=%s", status, body)
			}
			var got struct {
				evidence
				Probe *evidence `json:"probe"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			validate := func(item evidence) {
				t.Helper()
				if item.UID != 1001 || item.GID != 2001 || item.WorkingDir != "/app" || item.Marker != "container-contract" {
					t.Fatalf("process contract not preserved: %+v", item)
				}
				if options.Healthcheck && item.DeploymentMarker != "runtime-deployment" {
					t.Fatalf("deployment environment missing: %+v", item)
				}
			}
			validate(got.evidence)
			if !options.Healthcheck {
				return 0
			}
			if got.Probe != nil && got.Probe.Count > minProbeCount {
				validate(*got.Probe)
				if got.Cgroup == "" || got.Probe.Cgroup != got.Cgroup {
					t.Fatalf("probe resource scope differs from main: main=%q probe=%q", got.Cgroup, got.Probe.Cgroup)
				}
				if options.Companion && !strings.Contains(got.Cgroup, "/main-app") {
					t.Fatalf("companion deployment did not partition main workload: %q", got.Cgroup)
				}
				return got.Probe.Count
			}
			if time.Now().After(deadline) {
				t.Fatalf("OCI healthcheck did not advance beyond count %d: %s", minProbeCount, body)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	checkProcess(0)

	parkCtx, parkCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer parkCancel()
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/oci-fullrootfs/park", nil); status != http.StatusNoContent {
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

	restoredCount := checkProcess(0)
	if options.Healthcheck {
		// Require a probe after this post-wake observation; a count carried
		// in the snapshot alone must not satisfy restored polling coverage.
		checkProcess(restoredCount)
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cleanupCancel()
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/oci-fullrootfs/park", nil); status != http.StatusNoContent {
		t.Fatalf("cleanup park: status=%d", status)
	}
	if _, err := e2etest.WaitForInstanceState(cleanupCtx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
		t.Fatalf("direct OCI cleanup park: %v", err)
	}
}
