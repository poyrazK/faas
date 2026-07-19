// ResidentBytes is the cross-platform counterpart to listTenantScopes —
// sums the cgroup memory.current for each vm-*.scope leaf under the
// tenant slice, returning a map of instance name -> bytes. The vmmd
// gRPC Stats handler reads this; pkg/leakcheck/leakcheck.go reuses the
// same path for the leak invariant.
//
// On non-Linux hosts ResidentBytes returns (nil, false) — callers must
// treat that as "no data", not "zero bytes everywhere".

package leakcheck

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ResidentBytes enumerates the running per-VM cgroup leaves that
// jailer created under faas-tenant.slice (one per live VM the vmmd
// owns) and reads each memory.current file. The boolean is false on
// non-Linux hosts. Scope name equals the Lease.Instance — see
// pkg/fcvm.PerInstanceScope for the lockstep definition (instance
// verbatim because jailer v1.7 rejects '.' in --id). System slices
// (user.slice, system.slice, *.mount, init.scope, buildkit) that
// systemd installs as siblings of our scopes are filtered out — we
// only count what jailer created.
func ResidentBytes() (map[string]int64, bool) {
	if runtime.GOOS != "linux" {
		return nil, false
	}
	scopes := listTenantScopes()
	out := make(map[string]int64, len(scopes))
	for _, scope := range scopes {
		base := strings.TrimPrefix(scope, "/sys/fs/cgroup/faas-tenant.slice/")
		name := strings.TrimPrefix(base, "/")
		cur := filepath.Join(scope, "memory.current")
		b, err := readCgroupInt(cur)
		if err != nil {
			// Scope still listed but unreadable (race during destroy) —
			// skip rather than zero, which would under-report resident bytes.
			continue
		}
		out[name] = b
	}
	return out, true
}

// listTenantScopes enumerates only the cgroup leaves that jailer
// created for per-VM scopes. We rely on two markers that systemd and
// kernel-installed siblings do NOT carry: every vmm-created scope has a
// cpu.weight file (because we always pass `jailer --cgroup
// cpu.weight=N`), and exists at /sys/fs/cgroup/faas-tenant.slice/<id>
// with a depth-1 basename that is a valid Lease.Instance (the set of
// vmm instances — managed by pkg/fcvm.Allocator, fed by pkg/state).
//
// In practice: filter out anything with a "." in its name (kernel /
// systemd scratch dirs like "init.scope", "user.slice", "*.mount") and
// anything that contains a ".." (paranoia). Sibling tenants (a control
// plane slice, an unrelated subtree) end at a depth-of-2 with their
// own children; those children aren't ours either, but at the
// faas-tenant.slice boundary this filter is sufficient because we don't
// recursively walk — `os.ReadDir` returns one level.
//
// The path is mounted via cgroupv2 (the cgroups_v2 role in
// deploy/ansible asserts this). Implemented in this file (not the
// metal-only leakcheck.go) so the Stats handler can call it on every
// Linux build, not only on metal.
func listTenantScopes() []string {
	const base = "/sys/fs/cgroup/faas-tenant.slice"
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// systemd / kernel slices always contain a dot ("init.scope",
		// "user.slice", "system.slice", "*.mount"). The vmm jailer's
		// scope directory names — Lease.Instance — never do (the
		// alloc enforces [a-z0-9-]+). Skipping dot-bearing names keeps
		// us in sync with the production naming without a separate
		// marker file.
		if strings.Contains(name, ".") {
			continue
		}
		out = append(out, filepath.Join(base, name))
	}
	return out
}

// readCgroupInt reads one cgroupv2 file containing an integer in bytes
// (memory.current, memory.max, etc.) and returns it as int64.
func readCgroupInt(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}
