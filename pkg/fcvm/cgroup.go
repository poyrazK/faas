package fcvm

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// cgroupRoot is the canonical cgroup v2 unified mount (spec §3 ADR-008:
// cgroups v1 must be off). Package-level (not const) so cgroup_test.go
// can substitute t.TempDir() under a t.Cleanup. Production callers
// never touch this — they read /sys/fs/cgroup directly.
var cgroupRoot = "/sys/fs/cgroup"

// writeMemoryMax sets memory.max on the per-VM cgroup scope jailer
// creates during Boot/Restore (--parent-cgroup faas-tenant.slice with
// `jailer --cgroup cpu.weight=N`). Spec §4.4 line 137: "cgroup v2 scope
// faas-tenant.slice/{instance} with memory.max = plan_mb + 8 MB". The
// scope name equals the Lease.Instance verbatim — see
// PerInstanceScope for the lockstep definition. The +8 MB is the
// per-VM overhead accounted for by api.PerVMOverheadMB (pkg/api/limits.go).
// Note: the original spec text used `vm-{instance}.scope`; jailer
// v1.7's --id validator rejects '.' (panic: "Invalid char (.) at
// position N"), so we use the bare instance name and rely on the
// filter in pkg/fcvm/leakcheck/residentbytes.go to exclude
// systemd-installed siblings (init.scope, user.slice, etc.).
//
// The scope MUST already exist by the time this runs: Manager.Wake
// calls writeMemoryMax only after bringUp returns successfully, and
// bringUp blocks on firecracker readiness (which means jailer has
// already joined the scope). If the scope is absent, the IsNotExist
// branch produces a clear diagnostic that names the missing scope —
// distinct from a generic permission failure, so on-metal diagnosis
// doesn't waste time guessing.
//
// The write itself is naturally idempotent: cgroupv2 accepts a new
// memory.max with the same value as a no-op. Snapshot-restore Wake
// can call this on every wake without a separate reset (unlike tc
// qdisc, which collides).
func writeMemoryMax(instance string, planMB int) error {
	if planMB < 1 {
		return fmt.Errorf("fcvm: cgroup: planMB %d < 1", planMB)
	}
	bytes := int64(api.BillableRAMMB(planMB)) << 20
	scope := filepath.Join(cgroupRoot, ParentCgroup, PerInstanceScope(instance))
	path := filepath.Join(scope, "memory.max")
	// Newline-terminated: matches the kernel parser's expectation and
	// mirrors what systemd-run writes.
	body := fmt.Sprintf("%d\n", bytes)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("fcvm: cgroup: scope %s missing (jailer did not create it): %w", scope, err)
		}
		return fmt.Errorf("fcvm: cgroup: write %s: %w", path, err)
	}
	return nil
}
