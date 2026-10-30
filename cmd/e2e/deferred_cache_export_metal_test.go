//go:build metal

// deferred_cache_export_metal_test.go — ADR-20261029171525 on a real
// Firecracker stack, with builderd's FAAS_BUILD_DEFER_CACHE_EXPORT on:
//
//	deploy #1  → the build VM exports the image only and reports the
//	             dependency cache export owed; the deployment goes live
//	           → builderd boots build-<id>-cache on the kept drive, which
//	             exports and publishes the cache, then drops the drive
//	deploy #2  → same dependencies: the build imports the cache the second
//	             VM published and keeps it, and the app serves
//
// The second deploy is the proof that the second VM's export is usable: the
// first build exported no cache of its own, so a cache that the second
// build recognises can only have come from the export VM.
package e2e_test

// adr: 20261029171525

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeferredCacheExportMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_TEST_KERNEL / FAAS_BUILDER_BASE_PATH unset; skipping metal deferred cache export test")
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
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	h := e2etest.Start(t, pool, e2etest.All, builderd.DeferCacheExportEnv+"=1")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanPro)
	driveDir := filepath.Join(h.TmpDir, "drive")

	const slug = "deferapp"
	public := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &public}); got != http.StatusCreated {
		t.Fatalf("create app %s: status=%d", slug, got)
	}
	if _, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{RequireAuthn: &public,
		PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}}); status != http.StatusOK {
		t.Fatalf("open app auth %s: status=%d", slug, status)
	}
	first := deploySourceAndWait(t, h, pool, key, slug, NodeFixture(t))
	t.Run("image-handed-off-before-cache-export", func(t *testing.T) {
		// builderd writes this line only when the guest reported the export
		// deferred. (build-done.json itself may already be swept by now.)
		if log := deploymentBuildLog(t, pool, first.deploymentID); !strings.Contains(log, "dependency cache will be saved after the image is handed off") {
			t.Fatalf("first build log does not report the owed export:\n%s", log)
		}
	})
	t.Run("export-vm-publishes-and-drops-the-drive", func(t *testing.T) {
		waitForNoBuildDrives(t, driveDir)
		published, err := filepath.Glob(filepath.Join(driveDir, "dev-dependency-cache", "*", "index.json"))
		if err != nil || len(published) == 0 {
			t.Fatalf("no dependency cache published under %s (%v); first build log:\n%s", driveDir, err, deploymentBuildLog(t, pool, first.deploymentID))
		}
	})

	second := deploySourceAndWait(t, h, pool, key, slug, NodeFixture(t))
	t.Run("next-build-keeps-the-exported-cache", func(t *testing.T) {
		if log := deploymentBuildLog(t, pool, second.deploymentID); !strings.Contains(log, "dependencies unchanged — kept the dependency cache") {
			t.Fatalf("second build did not keep the exported cache; build log:\n%s", log)
		}
		assertHostingRuntime(t, h, pool, second, "node", "hello from faas (node fixture)\n")
		waitForNoBuildDrives(t, driveDir)
	})
}

// deferredExportStallWindow replaces the harness's five-minute stall window.
// With the export deferred, imaged scans and primes the deployment while the
// export VM still runs on the same node. Imaging moves no deployment row until
// it finishes, and on a two-vCPU acceptance host sharing its CPU with that
// VM it has taken longer than five minutes.
const deferredExportStallWindow = 12 * time.Minute

// deploySourceAndWait uploads a source deployment to an existing app and waits
// for it to go live.
func deploySourceAndWait(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key, slug string, sourceTar []byte) buildResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), sourceDeployCtxTimeout())
	defer cancel()
	body, status := postMultipartDeployment(t, h, key, slug, sourceTar, false, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy %s: status=%d body=%s", slug, status, body)
	}
	depID, buildID := parseQueuedDeployment(t, body)
	_, build, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, deferredExportStallWindow, e2etest.DefaultBuildCeiling)
	if err != nil {
		t.Fatalf("deploy %s did not go live: %v", slug, err)
	}
	if build.ID != buildID || build.Status != state.BuildSucceeded {
		t.Fatalf("deploy %s: build %s is %s, want %s succeeded", slug, build.ID, build.Status, buildID)
	}
	return buildResult{deploymentID: depID, buildID: buildID, slug: slug}
}

func deploymentBuildLog(t *testing.T, pool *pgxpool.Pool, deploymentID string) string {
	t.Helper()
	var log string
	if err := pool.QueryRow(context.Background(), `select coalesce(string_agg(line, '' order by seq), '') from deployment_logs where deployment_id = $1 and stream = 'build'`,
		deploymentID).Scan(&log); err != nil {
		t.Fatalf("read build log of %s: %v", deploymentID, err)
	}
	return log
}

// waitForNoBuildDrives waits for every build drive in builderd's drive
// directory to be removed. A deferred build's drive outlives its deployment
// going live until the cache export VM has finished with it.
func waitForNoBuildDrives(t *testing.T, driveDir string) {
	t.Helper()
	deadline := time.Now().Add(time.Duration(api.BuildCacheExportTimeoutSeconds)*time.Second + 2*time.Minute)
	for {
		drives, err := filepath.Glob(filepath.Join(driveDir, "*.ext4"))
		if err != nil {
			t.Fatal(err)
		}
		if len(drives) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("build drives still present after the cache export budget: %v", drives)
		}
		time.Sleep(2 * time.Second)
	}
}
