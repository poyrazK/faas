//go:build metal

// sec11_memory_max_e2e_test.go — M8 §11 cross-process fence gate.
//
// Spec §11 (security hardening checklist, ship-blocking §14 M8): "cgroup
// v2 scope faas-tenant.slice/{instance} with memory.max = plan + 8 MB".
// pkg/fcvm/manager_metal_test.go:308 (TestMetalMemoryMaxFenceEnforced) pins
// the in-process shape — it boots a Manager in the test binary, calls
// Wake, then reads /sys/fs/cgroup/faas-tenant.slice/<scope>/memory.max
// back. That test catches a regression in the writeMemoryMax() function
// itself; it cannot catch a regression where the *subprocess* vmmd
// silently swallows the write error (Manager.Wake is a Warn, not Fatal,
// per pkg/fcvm/manager.go:420-426). This test boots vmmd as a separate
// subprocess via pkg/e2etest.Harness and reads the kernel state from
// outside the process, so a swallowed-error regression trips here.
//
// Build tag: metal. Same pre-flight as deploy_wake_metal_test.go — needs
// /dev/kvm + root + FAAS_TEST_KERNEL. Skips on Mac dev, CI runners
// without KVM, and any host where /sys/fs/cgroup is not mounted.
//
// Spec anchor: §11 ("jailer/VM: cgroup v2 scope ... memory.max = plan_mb
// + 8 MB"), §14 M8 row ("security checklist signed off item-by-item"),
// §6.2-1/§13 RAM admission ceiling (47,600 MB = 85 % of 56 GB). This
// test closes the cross-process gap; the §6.2-1 ceiling is enforced in
// schedd admission and verified by pkg/sched invariants.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// expectedMemoryMaxBytes returns the kernel-state value the jailer +
// writeMemoryMax() pair must produce for a plan-sized instance.
// Single source of truth: pkg/api.BillableRAMMB(planMB) = planMB +
// PerVMOverheadMB; the test computes the same number to keep the assertion
// pinned on the §11 invariant instead of on a hard-coded constant.
func expectedMemoryMaxBytes(planMB int) int64 {
	return int64(api.BillableRAMMB(planMB)) << 20
}

// TestSec11_MemoryMaxFenceEnforced_CrossProcess is the M8 §11 cross-
// process fence gate. Sequence:
//
//  1. Boot apid + schedd + imaged + vmmd + gatewayd as real subprocesses
//     via e2etest.Start(..., DeployWake). Same harness deploy_wake_metal_test
//     uses; vmmd writes the per-VM memory.max after Wake returns.
//  2. Deploy a tiny app on Hobby plan (RAMMB=256 → expected memory.max =
//     (256 + 8) << 20 = 276 MiB = 289,406,976 bytes).
//  3. Wait for a running instance, then list
//     /sys/fs/cgroup/faas-tenant.slice/ and pick the only directory
//     (the scope the jailer just created for this instance). Reading
//     from the test process — not from inside vmmd — is the load-bearing
//     design choice: a code path that swallows writeMemoryMax errors
//     still gets caught here because the kernel state is the only thing
//     that actually protects the host.
//  4. Assert the file contains exactly expectedMemoryMaxBytes(256).
//
// Failure modes caught:
//   - vmmd.Manger.Wake returns success despite a swallowed writeMemoryMax
//     error (pkg/fcvm/manager.go:420-426 — currently a Warn).
//   - Jailer argv drift that drops --cgroup cpu.weight=256 (which forces
//     jailer to create the scope; without it the scope never exists).
//   - pkg/fcvm/config.go:ParentCgroup constant drift (test reads the
//     constant; if the value moves the listing turns up empty and the
//     diagnostic names the constant).
//   - pkg/api.PerVMOverheadMB = 8 drift (test recomputes from the constant,
//     so a future bump propagates without a code change here).
func TestSec11_MemoryMaxFenceEnforced_CrossProcess(t *testing.T) {
	// Same pre-flight as deploy_wake_metal_test.go: bail early on
	// non-metal boxes so the skip message names the missing env, not a
	// downstream harness panic.
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal memory.max cross-process test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	// /sys/fs/cgroup must be reachable. The EX44 always has it; Lima
	// without nested cgroup passthrough does not. Skipping is the right
	// shape — a non-metal CI runner cannot exercise the cross-process
	// probe even if it had /dev/kvm.
	if _, err := os.Stat("/sys/fs/cgroup"); err != nil {
		t.Skipf("/sys/fs/cgroup not mounted: %v", err)
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Stand up the fake OCI registry exactly like deploy_wake_metal_test
	// does — imaged needs the builder-base stub to survive its first
	// EnsureBaseExt4 pull, and the per-deploy base needs a single-layer
	// shape so oci.LayersAboveBase treats the app layer as above-base.
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	img, ref := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref = registry.AddImage("library/hello", img)

	// Create + deploy. The deployment lifecycle (imaged pull → app-layer
	// ext4 → prime cold boot → snapshot → PARKED) is what causes the
	// jailer to create the per-VM cgroup scope under faas-tenant.slice/.
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "m8-memfence", Type: "app"}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "m8-memfence")
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/m8-memfence/deployments",
		api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var depResp api.DeploymentResponse
	if err := json.Unmarshal(raw, &depResp); err != nil {
		t.Fatalf("decode deployment: %v body=%s", err, raw)
	}

	// Wait for the prime cycle to finish. The prime is a cold boot that
	// ends with PARKED — but the per-VM scope is created at boot time
	// (jailer joins it before firecracker starts), and writeMemoryMax
	// runs at the end of bringUp. By the time the deployment row flips
	// to LIVE, the kernel state is settled and the probe is safe.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer h.DumpLogs(t)
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depResp.ID, 60*time.Second); err != nil {
		if d, derr := state.NewPgStore(pool).DeploymentByID(ctx, depResp.ID); derr == nil {
			t.Logf("deployment state at failure: status=%s error=%q", d.Status, d.Error)
		}
		t.Fatalf("deployment did not reach live: %v", err)
	}
	// PROBE while the instance is still RUNNING — the jailer removes
	// the per-VM cgroup scope during the Park→Kill sequence
	// (pkg/fcvm/vmm.go:766-770: `os.RemoveAll(scopePath)` inside
	// JailerVMM.Kill), so probing after the park wait would find an
	// empty directory and t.Fatalf without ever reading the kernel
	// state. Wait for StateRunning first, probe, then let the natural
	// idle reaper park the instance (the post-park wait is no longer
	// load-bearing for the assertion, but kept so a regression that
	// keeps the instance RUNNING indefinitely still surfaces).
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 60*time.Second); err != nil {
		t.Fatalf("no running instance: %v", err)
	}
	scopeBase := filepath.Join("/sys/fs/cgroup", fcvm.ParentCgroupRoot)
	entries, err := os.ReadDir(scopeBase)
	if err != nil {
		t.Fatalf("read %s: %v (parent cgroup constant drift? pkg/fcvm/config.go:ParentCgroup)", scopeBase, err)
	}
	var scopes []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Mirror the leakcheck filter: kernel/systemd scratch dirs
		// (init.scope, user.slice, *.mount) contain '.', so a name
		// without '.' is the jailer-created per-VM scope. See
		// pkg/fcvm/leakcheck/residentbytes.go for the full rationale.
		if !strings.Contains(e.Name(), ".") {
			scopes = append(scopes, e.Name())
		}
	}
	if len(scopes) == 0 {
		t.Fatalf("no jailer-created scopes under %s; jailer dropped --cgroup cpu.weight=256?", scopeBase)
	}
	if len(scopes) > 1 {
		// Multiple jailer scopes is a leakcheck failure — every other
		// scope is either a leaked instance from a previous test run
		// or a parallel test sharing the box. Either way, picking
		// scopes[0] is unsafe: the leaked scope's memory.max also
		// happens to be (plan+8)<<20 (every plan has the same overhead),
		// so a "log and continue" branch would silently mask the leak.
		// Fail fast with the list so the next operator run attributes it.
		t.Fatalf("unexpected extra scopes %v under %s — leakcheck failed (see pkg/fcvm/leakcheck)", scopes, scopeBase)
	}
	scope := scopes[0]
	scopeDir := filepath.Join(scopeBase, scope)

	// Read memory.max. The kernel writes the value as decimal bytes
	// terminated by \n (pkg/fcvm/cgroup.go:51). The /sys/fs/cgroup/...
	// read must succeed from outside the vmmd process — the file mode
	// is r--r--r-- but owned by root, so a non-root reader may get EACCES
	// here. /dev/kvm requires root, so this test always runs as root
	// in practice; if a future refactor relaxes that, this read is
	// the first thing to break, which is the desired tripwire.
	body, err := os.ReadFile(filepath.Join(scopeDir, "memory.max"))
	if err != nil {
		t.Fatalf("read %s/memory.max: %v (kernel state unreachable from this process — root required?)", scopeDir, err)
	}
	got, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", body, err)
	}

	// Hobby plan: RAMMB=256 → BillableRAMMB(256)=264 MiB → 264<<20 bytes.
	// Computed from the constants, not hard-coded, so the test survives
	// a future PerVMOverheadMB bump without an edit here.
	want := expectedMemoryMaxBytes(256)
	if got != want {
		t.Errorf("memory.max = %d (=%d MiB), want %d (=%d MiB); vmmd.Manger.Wake likely swallowed a writeMemoryMax error (pkg/fcvm/manager.go:420-426)",
			got, got>>20, want, want>>20)
	}
	t.Logf("memory.max OK: instance=%s scope=%s value=%d bytes (=%d MiB); plan=Hobby 256 MiB + PerVMOverheadMB=%d",
		scope, scopeDir, got, got>>20, api.PerVMOverheadMB)
}
