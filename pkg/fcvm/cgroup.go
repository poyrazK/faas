package fcvm

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cgroupRoot is the canonical cgroup v2 unified mount (spec §3 ADR-008:
// cgroups v1 must be off). Package-level (not const) so cgroup_test.go
// can substitute t.TempDir() under a t.Cleanup. Production callers
// never touch this — they read /sys/fs/cgroup directly.
var cgroupRoot = "/sys/fs/cgroup"

// writePlanCgroup sets memory.max and cpu.max on the per-VM cgroup
// scope jailer creates during Boot/Restore (--parent-cgroup
// `faas-tenant.slice/<plan-slice>` with `jailer --cgroup cpu.weight=N`).
//
// Spec §4.4 line 137: "cgroup v2 scope faas-tenant.slice/{instance}
// with memory.max = plan_mb + 8 MB". Issue #301 / ADR-044 extends
// the hierarchy to 3 levels (`faas-tenant.slice/<plan-slice>/<instance>`)
// so the kernel can enforce per-plan cpu.weight + cpu.max quotas;
// the scope name still equals the Lease.Instance verbatim — see
// PerInstanceScope for the lockstep definition.
//
// The +8 MB is the per-VM overhead accounted for by
// api.PerVMOverheadMB (pkg/api/limits.go).
//
// Note: the original spec text used `vm-{instance}.scope`; jailer
// v1.7's --id validator rejects '.' (panic: "Invalid char (.) at
// position N"), so we use the bare instance name and rely on the
// filter in pkg/fcvm/leakcheck/residentbytes.go to exclude
// systemd-installed siblings (init.scope, user.slice, etc.).
//
// The scope MUST already exist by the time this runs: Manager.Wake
// calls writePlanCgroup only after bringUp returns successfully, and
// bringUp blocks on firecracker readiness (which means jailer has
// already joined the scope). If the scope is absent, the IsNotExist
// branch produces a clear diagnostic that names the missing scope —
// distinct from a generic permission failure, so on-metal diagnosis
// doesn't waste time guessing.
//
// Both writes are naturally idempotent: cgroupv2 accepts a new
// memory.max / cpu.max with the same value as a no-op. Snapshot-restore
// Wake can call this on every wake without a separate reset (unlike tc
// qdisc, which collides).
//
// cpu.max is a direct file write (not a jailer --cgroup arg) because
// jailer v1.7 only exposes cpu.weight and memory.max through --cgroup;
// the quota must land in cpu.max so the kernel enforces it.
//
// A zero CPU value is accepted for legacy callers and resolves to the plan
// quota.
func writeAppCgroup(instance string, plan api.Plan, planMB, cpuMillicores int) error {
	if planMB < 1 {
		return fmt.Errorf("fcvm: cgroup: planMB %d < 1", planMB)
	}
	if !plan.Valid() {
		return fmt.Errorf("fcvm: cgroup: invalid plan %q (issue #301 / ADR-044)", plan)
	}
	return writeAppCgroupAt(ParentCgroupFor(plan), instance, plan, planMB, cpuMillicores)
}
func writeAppCgroupAt(parent, instance string, plan api.Plan, planMB, cpuMillicores int) error {
	scope := filepath.Join(cgroupRoot, parent, PerInstanceScope(instance))
	if err := writeMemoryMaxTo(scope, planMB); err != nil {
		return err
	}
	if err := writeAppCPUMaxTo(scope, plan, cpuMillicores); err != nil {
		return err
	}
	return nil
}

// writeBuildCgroup applies the dedicated builder VM fence. Build VMs are
// fixed at 2 vCPU / 2048 MiB by the build contract, so their CPU quota is
// independent of the owning account's tenant plan quota.
func writeBuildCgroup(instance string, planMB int) error {
	if planMB < 1 {
		return fmt.Errorf("fcvm: cgroup: builder planMB %d < 1", planMB)
	}
	scope := filepath.Join(cgroupRoot, BuilderCgroupParent, PerInstanceScope(instance))
	if err := writeMemoryMaxAt(scope, api.BuilderMemoryMaxMB(planMB)); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(scope, "cpu.max"), []byte("200000 100000\n"), 0o644); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("fcvm: cgroup: builder scope %s missing: %w", scope, err)
		}
		return fmt.Errorf("fcvm: cgroup: write builder cpu.max: %w", err)
	}
	return nil
}

func writeMemoryMaxAt(scope string, memoryMB int) error {
	if memoryMB < 1 {
		return fmt.Errorf("fcvm: cgroup: memoryMB %d < 1", memoryMB)
	}
	bytes := int64(memoryMB) << 20
	path := filepath.Join(scope, "memory.max")
	body := fmt.Sprintf("%d\n", bytes)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("fcvm: cgroup: scope %s missing (jailer did not create it): %w", scope, err)
		}
		return fmt.Errorf("fcvm: cgroup: write %s: %w", path, err)
	}
	return nil
}

// writeMemoryMaxTo writes memory.max (in bytes) into the given
// fully-resolved scope path. Idempotent. Public so the cgroup unit
// test can exercise it directly without spinning up a Manager.
func writeMemoryMaxTo(scope string, planMB int) error {
	return writeMemoryMaxAt(scope, api.BillableRAMMB(planMB))
}

// widenSnapshotMemoryCgroup temporarily raises the app VM's host-side
// memory.max while Firecracker materialises a full snapshot. A normal app VM
// is fenced at ram_mb + PerVMOverheadMB, but /snapshot/create briefly needs
// additional host memory for its snapshot bookkeeping and copy-on-write
// accounting. Builders never use the app snapshot path and therefore do not
// receive this exception.
//
// The returned restore function is deliberately explicit: callers must keep
// the widened limit for the complete snapshot/export operation, then restore
// the ordinary per-VM fence before the VM resumes or is destroyed.
func widenSnapshotMemoryCgroup(l Lease) (func() error, error) {
	if l.IsBuilder || l.MemoryMaxMiB < 1 {
		// Builders are exported and destroyed rather than parked, while zero
		// memory is the legacy lease shape used by older unit-only callers.
		return func() error { return nil }, nil
	}

	scope := filepath.Join(cgroupRoot, ParentCgroupFor(l.Plan), PerInstanceScope(l.Instance))
	original := api.BillableRAMMB(l.MemoryMaxMiB)
	if err := writeMemoryMaxAt(scope, api.SnapshotMemoryMaxMB(l.MemoryMaxMiB)); err != nil {
		return nil, fmt.Errorf("fcvm: widen snapshot memory.max for %s: %w", l.Instance, err)
	}

	return func() error {
		if err := writeMemoryMaxAt(scope, original); err != nil {
			return fmt.Errorf("fcvm: restore snapshot memory.max for %s: %w", l.Instance, err)
		}
		return nil
	}, nil
}

// writeAppCPUMaxTo writes cpu.max (in microseconds) into the given
// fully-resolved scope path. Idempotent. Same Newline-terminated
// format as systemd-run — matches the kernel parser's expectation.
//
// cpu.max format is "<quota> <period>" microseconds. An empty plan
// row would write "0 100000" which the kernel treats as "no quota"
// (an unconstrained slice); we validate the plan above so this
// branch is unreachable in production.
func writeAppCPUMaxTo(scope string, plan api.Plan, cpuMillicores int) error {
	quota := plan.CPUQuotaUS()
	period := plan.CPUPeriodUS()
	if quota <= 0 || period <= 0 {
		// Fail closed: a missing quota is a missing-row plan,
		// which Manager.Wake rejected upstream. The check here
		// is defence-in-depth so a future caller that bypasses
		// Manager.Wake doesn't silently emit an unbounded slice.
		return fmt.Errorf("fcvm: cgroup: plan %q has non-positive cpu.max (%d/%d)", plan, quota, period)
	}
	if cpuMillicores > 0 {
		if !api.ValidAppCPUMillicores(cpuMillicores) {
			return fmt.Errorf("fcvm: cgroup: invalid cpu_millicores %d", cpuMillicores)
		}
		quota = cpuMillicores * period / 1000
	}
	path := filepath.Join(scope, "cpu.max")
	body := fmt.Sprintf("%d %d\n", quota, period)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("fcvm: cgroup: scope %s missing (jailer did not create it): %w", scope, err)
		}
		return fmt.Errorf("fcvm: cgroup: write %s: %w", path, err)
	}
	return nil
}

// writeMemoryMax is the legacy single-field writer kept so the
// cgroup_test.go unit tests can call the old signature without
// routing through a plan. It writes ONLY memory.max — cpu.max is
// the per-plan enforcement (issue #301 / ADR-044) and the legacy
// callers never had a plan to pass. New callers must use
// writePlanCgroup for the production path; this legacy shim
// exists for the unit-test surface only. The scope path uses the
// legacy 2-level parent (ParentCgroupRoot directly, no plan slice)
// to match what the pre-issue-301 unit tests assert.
func writeMemoryMax(instance string, planMB int) error {
	if planMB < 1 {
		return fmt.Errorf("fcvm: cgroup: planMB %d < 1", planMB)
	}
	scope := filepath.Join(cgroupRoot, ParentCgroupRoot, PerInstanceScope(instance))
	return writeMemoryMaxTo(scope, planMB)
}

// writeWorkloadCgroup (issue #463 / ADR-069 / PR-B) creates a child
// cgroup scope under the per-instance scope with optional memory.max
// and cpu.max limits. The parent scope's limits stay at the plan
// ceiling (writePlanCgroup wrote them); the kernel evaluates the
// innermost effective limits, so a sidecar child that hits either
// cap is contained while the main workload's child stays untouched.
//
// The child scope is a leaf — no processes are fork'd into it (the
// firecracker process remains in the parent scope set by jailer,
// and the guest's in-VM processes run inside the guest's own
// cgroup namespace which is invisible to the host). The leaf
// exists to enforce a defense-in-depth cap on the host-side
// firecracker process and to track per-workload memory events
// (memory.failcnt, memory.events) for triage. The AC #4 "sidecar
// OOM doesn't kill main" guarantee is primarily enforced inside
// the guest by guest-init's per-workload cgroup partition
// (guest/init/cgroup_partition_linux.go — wires mountCgroup2,
// partitionInto, placeIntoLeaf, and the per-workload
// memory.max + cgroup.procs writes; see also
// docs/adr/069-sidecar-containers-init-metrics-hard-cap-2.md).
// The host-side leaf is a second line of defense that matters
// when the host-side firecracker process itself runs away.
//
// workloadName is the leaf directory name (e.g. "main",
// "metrics", "logger"); we don't allow "/" or ".." in the name
// because the path is constructed by filepath.Join and any
// traversal would escape the parent scope. The defense is a
// simple reject — there's no benign caller that needs a path
// separator in a workload name.
//
// cpu.max uses the standard 100ms cgroup period. A millicore value
// is converted to a quota against that period (250m = 25000us),
// preserving the requested fraction of one host CPU independently
// of the parent plan's period.
//
// Idempotent: the same workloadName + limits pair produces a
// no-op write. The scope path is removed by vmm.Kill's
// os.RemoveAll(scopePath) on the parent, which cascades to
// children (Parent's cgroup.procs is empty once firecracker is
// reaped, so the kernel allows the child leaves to be removed).
// The leaf may sit unattached for a few hundred ms during Kill;
// that's fine — leakcheck is gate-time, not real-time.
func writeWorkloadCgroup(parentScope, workloadName string, ramMB int, cpuMillicoresOpt ...int) error {
	if workloadName == "" {
		return fmt.Errorf("fcvm: cgroup: workload name empty")
	}
	if strings.ContainsAny(workloadName, "/\\") || workloadName == "." || workloadName == ".." {
		return fmt.Errorf("fcvm: cgroup: workload name %q contains path separator", workloadName)
	}
	if ramMB < 0 {
		return fmt.Errorf("fcvm: cgroup: workload %q ramMB %d < 0", workloadName, ramMB)
	}
	cpuMillicores := 0
	if len(cpuMillicoresOpt) > 0 {
		cpuMillicores = cpuMillicoresOpt[0]
	}
	if cpuMillicores < 0 || (cpuMillicores != 0 && !api.ValidAppCPUMillicores(cpuMillicores)) {
		return fmt.Errorf("fcvm: cgroup: workload %q invalid cpu_millicores %d", workloadName, cpuMillicores)
	}
	if ramMB == 0 && cpuMillicores == 0 {
		return fmt.Errorf("fcvm: cgroup: workload %q has no memory or CPU limit", workloadName)
	}
	childScope := filepath.Join(parentScope, workloadName)
	if err := os.MkdirAll(childScope, 0o755); err != nil {
		return fmt.Errorf("fcvm: cgroup: mkdir %s: %w", childScope, err)
	}
	if ramMB > 0 {
		// Write memory.max directly (NOT via writeMemoryMaxTo). The
		// latter wraps input through BillableRAMMB which adds the
		// +8 MB per-VM overhead; that overhead is allocated ONLY on
		// the parent scope (it's the host-side firecracker process
		// overhead, not a per-workload surcharge).
		bytes := int64(ramMB) << 20
		path := filepath.Join(childScope, "memory.max")
		body := fmt.Sprintf("%d\n", bytes)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("fcvm: cgroup: child scope %s missing: %w", childScope, err)
			}
			return fmt.Errorf("fcvm: cgroup: write %s: %w", path, err)
		}
	} else {
		// A reused leaf may still carry a previous RAM override. Reset
		// it to the parent limit when the new workload inherits RAM.
		path := filepath.Join(childScope, "memory.max")
		if _, err := os.Stat(path); err == nil {
			if err := os.WriteFile(path, []byte("max\n"), 0o644); err != nil {
				return fmt.Errorf("fcvm: cgroup: reset %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("fcvm: cgroup: stat %s: %w", path, err)
		}
	}
	cpuPath := filepath.Join(childScope, "cpu.max")
	if cpuMillicores > 0 {
		const periodUS = 100_000
		quotaUS := cpuMillicores * periodUS / 1000
		body := fmt.Sprintf("%d %d\n", quotaUS, periodUS)
		if err := os.WriteFile(cpuPath, []byte(body), 0o644); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("fcvm: cgroup: child scope %s missing: %w", childScope, err)
			}
			return fmt.Errorf("fcvm: cgroup: write %s: %w", cpuPath, err)
		}
	} else if _, err := os.Stat(cpuPath); err == nil {
		// Reset a reused leaf so a previous CPU override cannot survive
		// a later wake that inherits the parent app quota.
		if err := os.WriteFile(cpuPath, []byte("max 100000\n"), 0o644); err != nil {
			return fmt.Errorf("fcvm: cgroup: reset %s: %w", cpuPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("fcvm: cgroup: stat %s: %w", cpuPath, err)
	}
	return nil
}

// removeWorkloadCgroups removes every workload child scope under
// the parent scope (issue #463 / ADR-069 / PR-B). Called by
// vmm.Kill AFTER the per-instance scope's cgroup.procs has been
// drained (the kernel cascade removes children automatically when
// the parent is removed via os.RemoveAll, but on a controller
// where the kernel hasn't yet flushed the parents's procs this
// explicit pre-remove shortens the leakage window for leakcheck).
//
// The function is best-effort: a missing child is fine (already
// removed by the kernel cascade), but a non-IsNotExist error is
// logged + swallowed so the teardown chain doesn't fail on a
// transient EBUSY. Returns the list of leaves that were
// successfully removed so the structured-log path can include
// the count without re-stat'ing the directory.
func removeWorkloadCgroups(parentScope string, workloadNames []string) []string {
	removed := make([]string, 0, len(workloadNames))
	for _, name := range workloadNames {
		if name == "" {
			continue
		}
		child := filepath.Join(parentScope, name)
		// Stat first so we can distinguish "was there, removed it"
		// from "already gone, no-op". The latter is the expected
		// shape on a second call (idempotent teardown) and on the
		// kernel-cascade path (parent removal already swept the
		// children). The structured-log "removed" list is reported
		// back to the caller for triage; including already-gone
		// names would inflate the count and obscure real signal.
		if _, err := os.Stat(child); err != nil {
			if !os.IsNotExist(err) {
				slog.Default().Warn("cgroup workload scope stat failed; continuing teardown",
					"path", child, "err", err)
			}
			continue
		}
		if err := os.RemoveAll(child); err != nil && !os.IsNotExist(err) {
			slog.Default().Warn("cgroup workload scope remove failed; continuing teardown",
				"path", child, "err", err)
			continue
		}
		removed = append(removed, name)
	}
	return removed
}
