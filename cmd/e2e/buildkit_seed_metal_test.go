//go:build metal && linux

// buildkit_seed_metal_test.go — ADR-20261030122204 on a real Firecracker
// stack, with builderd's FAAS_BUILD_BUILDKIT_SEED on:
//
//	deploy #1  → a Node app builds; builderd has no seed yet, so it is
//	             normally unseeded and pays the builder image pull
//	seed run   → once the builder slot is idle, builderd boots the seed run
//	             and keeps its drive2 as the node's seed
//	deploy #2  → another Node app (no dependency cache of its own) builds on a
//	             reflink copy of the seed: the guest reports BuildKit ran on
//	             it, the builder image step is a cache hit, and the app serves
//
// The build-drive directory must be on a reflink filesystem (XFS with
// reflink=1, as /srv/fc is on the production nodes). Elsewhere no build can
// get a copy, so the test skips.
package e2e_test

// adr: 20261030122204

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// buildKitSeededLine is what builderd writes to the build log when the guest
// reports BuildKit ran on the seed copy.
const buildKitSeededLine = "BuildKit started from this node's seed"

// buildKitSeedWait covers the first build finishing, the seed loop's next
// tick and a cold seed run on a two-vCPU acceptance host.
const buildKitSeedWait = 30 * time.Minute

func TestBuildKitSeedMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_TEST_KERNEL / FAAS_BUILDER_BASE_PATH unset; skipping metal BuildKit seed test")
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
	h := e2etest.Start(t, pool, e2etest.All, builderd.BuildKitSeedEnv+"=1")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	requireReflink(t, h.TmpDir)
	key := h.SeedAccount(context.Background(), api.PlanPro)
	driveDir := filepath.Join(h.TmpDir, "drive")
	seedDir := filepath.Join(h.TmpDir, "buildkit-seed")

	createOpenApp(t, h, key, "seedless")
	first := deploySourceAndWait(t, h, pool, key, "seedless", NodeFixture(t))
	firstLog := deploymentBuildLog(t, pool, first.deploymentID)
	firstStep, firstStepSeen := builderImageStep(firstLog)
	t.Logf("first build: seeded=%v, builder image step %v (in slowest steps: %v)",
		strings.Contains(firstLog, buildKitSeededLine), firstStep, firstStepSeen)

	t.Run("seed-run-makes-a-seed", func(t *testing.T) {
		waitForBuildKitSeed(t, seedDir)
	})

	createOpenApp(t, h, key, "seeded")
	second := deploySourceAndWait(t, h, pool, key, "seeded", NodeFixture(t))
	t.Run("seeded-build-hits-the-builder-image", func(t *testing.T) {
		log := deploymentBuildLog(t, pool, second.deploymentID)
		if !strings.Contains(log, buildKitSeededLine) {
			t.Fatalf("second build did not run on the seed; build log:\n%s", log)
		}
		if step, ok := builderImageStep(log); ok && step >= 10*time.Second {
			t.Fatalf("seeded build still spent %v on the builder image (unseeded first build: %v); build log:\n%s", step, firstStep, log)
		}
		assertHostingRuntime(t, h, pool, second, "node", "hello from faas (node fixture)\n")
	})
	t.Run("copies-go-and-the-seed-stays", func(t *testing.T) {
		waitForNoBuildDrives(t, driveDir)
		ready, err := filepath.Glob(filepath.Join(seedDir, "*.ready"))
		if err != nil || len(ready) != 1 {
			t.Fatalf("seed markers after the builds = %v (%v), want one", ready, err)
		}
	})
}

func createOpenApp(t *testing.T, h *e2etest.Harness, key, slug string) {
	t.Helper()
	public := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &public}); got != http.StatusCreated {
		t.Fatalf("create app %s: status=%d", slug, got)
	}
	if _, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{RequireAuthn: &public,
		PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}}); status != http.StatusOK {
		t.Fatalf("open app auth %s: status=%d", slug, status)
	}
}

// requireReflink skips the test unless dir's filesystem can make a FICLONE
// copy, which builderd needs to give a build its copy of the seed.
func requireReflink(t *testing.T, dir string) {
	t.Helper()
	src, err := os.CreateTemp(dir, "reflink-probe-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(src.Name()) }()
	defer func() { _ = src.Close() }()
	if _, err := src.WriteString("probe"); err != nil {
		t.Fatal(err)
	}
	dst, err := os.CreateTemp(dir, "reflink-probe-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(dst.Name()) }()
	defer func() { _ = dst.Close() }()
	if err := unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())); err != nil {
		t.Skipf("%s cannot make reflink copies (%v); builds get no seed copy there", dir, err)
	}
}

// waitForBuildKitSeed waits for builderd to mark a seed ready.
func waitForBuildKitSeed(t *testing.T, seedDir string) {
	t.Helper()
	deadline := time.Now().Add(buildKitSeedWait)
	for {
		ready, err := filepath.Glob(filepath.Join(seedDir, "*.ready"))
		if err != nil {
			t.Fatal(err)
		}
		if len(ready) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no BuildKit seed in %s after %v", seedDir, buildKitSeedWait)
		}
		time.Sleep(5 * time.Second)
	}
}

// builderImageStep is the time the build log's "slowest build steps" list
// gives the Railpack builder image, if it lists it.
func builderImageStep(log string) (time.Duration, bool) {
	inSteps := false
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "[build] slowest build steps:") {
			inSteps = true
			continue
		}
		if !inSteps {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			inSteps = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(line, "railpack-builder") {
			continue
		}
		if d, err := time.ParseDuration(fields[0]); err == nil {
			return d, true
		}
	}
	return 0, false
}
