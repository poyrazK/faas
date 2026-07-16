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

// ResidentBytes enumerates the running vm-*.scope cgroups and reads each
// memory.current file. The boolean is false on non-Linux hosts.
func ResidentBytes() (map[string]int64, bool) {
	if runtime.GOOS != "linux" {
		return nil, false
	}
	scopes := listTenantScopes()
	out := make(map[string]int64, len(scopes))
	for _, scope := range scopes {
		base := strings.TrimPrefix(scope, "/sys/fs/cgroup/faas-tenant.slice/")
		base = strings.TrimSuffix(base, ".scope")
		name := strings.TrimPrefix(base, "vm-")
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

// listTenantScopes globs vm-*.scope under faas-tenant.slice. The path
// is mounted via cgroupv2 (the cgroups_v2 role in deploy/ansible asserts
// this). Implemented in this file (not the metal-only leakcheck.go) so
// the Stats handler can call it on every Linux build, not only on metal.
func listTenantScopes() []string {
	base := "/sys/fs/cgroup/faas-tenant.slice"
	entries, err := filepath.Glob(filepath.Join(base, "vm-*.scope"))
	if err != nil {
		return nil
	}
	return entries
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
