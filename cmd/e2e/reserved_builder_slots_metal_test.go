//go:build metal && linux

// reserved_builder_slots_metal_test.go — ADR-20261118154632 amendment 1 on a
// real Firecracker stack. A node provisioned with two reserved builder slots
// (FAAS_BUILDER_RESERVED_SLOTS=2, faas-cp-build.slice sized by
// api.BuilderSliceMemoryMaxMB(2)) builds two source deploys at once with
// builder_elastic_slots off: both take guaranteed slots, two builder VMs run
// in faas-cp-build.slice together, neither is OOM-killed, and both scopes go
// away afterwards.
package e2e_test

// adr: 20261118154632

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderslots"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

const buildSliceDir = "/sys/fs/cgroup/faas.slice/faas-cp.slice/faas-cp-build.slice"

func TestReservedBuilderSlotsMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_TEST_KERNEL / FAAS_BUILDER_BASE_PATH unset; skipping metal reserved builder slot test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if _, err := os.Stat(buildSliceDir); err != nil {
		t.Skipf("no faas-cp-build.slice on this host: %v", err)
	}
	const reserved = 2
	sizeSlicesFor(t, reserved)
	oomAtStart := sliceOOMKills(t)

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.All, fmt.Sprintf("%s=%d", builderslots.ReservedSlotsEnv, reserved))
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanPro)
	createOpenApp(t, h, key, "reserved-a")
	createOpenApp(t, h, key, "reserved-b")

	watchCtx, stopWatch := context.WithCancel(ctx)
	peak := make(chan int, 1)
	go watchBuildSliceScopes(watchCtx, peak)

	type deployed struct{ slug, depID, buildID string }
	var deploys []deployed
	for _, slug := range []string{"reserved-a", "reserved-b"} {
		body, status := postMultipartDeployment(t, h, key, slug, NodeFixture(t), false, "")
		if status != http.StatusAccepted {
			t.Fatalf("deploy %s: status=%d body=%s", slug, status, body)
		}
		depID, buildID := parseQueuedDeployment(t, body)
		deploys = append(deploys, deployed{slug: slug, depID: depID, buildID: buildID})
	}
	var wg sync.WaitGroup
	errs := make([]error, len(deploys))
	for i, d := range deploys {
		wg.Add(1)
		go func(i int, d deployed) {
			defer wg.Done()
			waitCtx, cancel := context.WithTimeout(ctx, sourceDeployCtxTimeout())
			defer cancel()
			_, build, err := e2etest.WaitForSourceDeployment(waitCtx, t, pool, d.depID, deferredExportStallWindow, e2etest.DefaultBuildCeiling)
			if err == nil && (build.ID != d.buildID || build.Status != state.BuildSucceeded) {
				err = errors.New("build " + build.ID + " is " + string(build.Status))
			}
			errs[i] = err
		}(i, d)
	}
	wg.Wait()
	stopWatch()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("deploy %s did not go live: %v", deploys[i].slug, err)
		}
	}

	t.Run("both-builds-guaranteed-and-concurrent", func(t *testing.T) {
		for _, d := range deploys {
			if log := deploymentBuildLog(t, pool, d.depID); !strings.Contains(log, "allocated builder slot (guaranteed)") {
				t.Fatalf("deploy %s did not run on a guaranteed slot; build log:\n%s", d.slug, log)
			}
		}
		if got := <-peak; got < reserved {
			t.Fatalf("at most %d builder VMs ran in faas-cp-build.slice at once, want %d", got, reserved)
		}
	})
	t.Run("no-slice-oom-kill", func(t *testing.T) {
		if got := sliceOOMKills(t); got != oomAtStart {
			t.Fatalf("faas-cp-build.slice oom_kill went %d -> %d", oomAtStart, got)
		}
	})
	t.Run("builder-scopes-cleaned-up", func(t *testing.T) {
		deadline := time.Now().Add(time.Minute)
		for {
			scopes := borrowedScopes(buildSliceDir)
			if len(scopes) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("builder scopes left behind in faas-cp-build.slice: %v", scopes)
			}
			time.Sleep(time.Second)
		}
	})
}

// sizeSlicesFor gives faas-cp-build.slice and faas-cp.slice the fences
// node_join writes for reserved slots, for this boot only, and puts the
// previous values back afterwards.
func sizeSlicesFor(t *testing.T, reserved int) {
	t.Helper()
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skipf("systemctl not available: %v", err)
	}
	show := func(unit, prop string) string {
		out, err := exec.Command("systemctl", "show", "-p", prop, "--value", unit).Output()
		if err != nil {
			t.Skipf("systemctl show %s %s: %v", unit, prop, err)
		}
		return strings.TrimSpace(string(out))
	}
	set := func(unit string, props ...string) {
		args := append([]string{"set-property", "--runtime", unit}, props...)
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			t.Fatalf("systemctl %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	prevCP := show("faas-cp.slice", "MemoryMax")
	prevBuild := show("faas-cp-build.slice", "MemoryMax")
	prevQuota := show("faas-cp-build.slice", "CPUQuotaPerSecUSec")
	set("faas-cp.slice", fmt.Sprintf("MemoryMax=%dM", api.ComputeCPSliceMemoryMaxMB(reserved)))
	set("faas-cp-build.slice", fmt.Sprintf("MemoryMax=%dM", api.BuilderSliceMemoryMaxMB(reserved)), fmt.Sprintf("CPUQuota=%d%%", api.BuilderSliceCPUQuotaPct(reserved)))
	t.Cleanup(func() {
		// systemd reports CPUQuota as run time per second ("2s" is 200%) or
		// "infinity"; an empty CPUQuota= removes the quota.
		quota := "CPUQuota="
		if secs, err := time.ParseDuration(prevQuota); err == nil && secs > 0 {
			quota = fmt.Sprintf("CPUQuota=%d%%", int(secs.Seconds()*100))
		}
		props := []string{"MemoryMax=" + prevBuild, quota}
		set("faas-cp-build.slice", props...)
		set("faas-cp.slice", "MemoryMax="+prevCP)
	})
}

// sliceOOMKills reads faas-cp-build.slice's cumulative oom_kill counter.
func sliceOOMKills(t *testing.T) uint64 {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(buildSliceDir, "memory.events"))
	if err != nil {
		t.Fatalf("read memory.events: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "oom_kill" {
			n, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				t.Fatalf("parse oom_kill: %v", err)
			}
			return n
		}
	}
	t.Fatal("memory.events has no oom_kill")
	return 0
}

// watchBuildSliceScopes reports the most builder scopes that held a process
// in faas-cp-build.slice at the same time.
func watchBuildSliceScopes(ctx context.Context, peak chan<- int) {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	most := 0
	for {
		select {
		case <-ctx.Done():
			peak <- most
			return
		case <-tick.C:
			running := 0
			for _, scope := range borrowedScopes(buildSliceDir) {
				procs, err := os.ReadFile(filepath.Join(buildSliceDir, scope, "cgroup.procs"))
				if err == nil && strings.TrimSpace(string(procs)) != "" {
					running++
				}
			}
			most = max(most, running)
		}
	}
}
