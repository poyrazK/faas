package fcvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// enableAttempt reports the body ensureSubtreeControllers wrote for a single
// controller against a fresh parent, or "" when it wrote nothing.
//
// A regular file cannot emulate cgroupfs accumulate semantics (each write
// truncates), so each controller is probed in isolation rather than by reading
// a shared parent back.
func enableAttempt(t *testing.T, controllers, alreadyEnabled, want string) string {
	t.Helper()
	parent := t.TempDir()
	writeCgroupFile(t, parent, "cgroup.controllers", controllers)
	writeCgroupFile(t, parent, "cgroup.subtree_control", alreadyEnabled)
	if err := ensureSubtreeControllers(parent, []string{want}); err != nil {
		t.Fatalf("ensureSubtreeControllers(%q): %v", want, err)
	}
	got := readCgroupFile(t, parent, "cgroup.subtree_control")
	if got == alreadyEnabled {
		return ""
	}
	return strings.TrimSpace(got)
}

// TestEnsureSubtreeControllers_EnablesMissingControllers pins the fix for the
// production defect where every per-instance cgroup had no controller files.
//
// In cgroup v2 a child only gets a controller's interface files when its
// PARENT lists that controller in cgroup.subtree_control. The production
// fleet showed:
//
//	faas-tenant-scale.slice  controllers=[cpu memory pids] subtree_control=[]
//
// so no per-VM memory.max existed, the §11 fence was silently absent, and
// vmmd's write failed with a misleading "permission denied" — open(2) cannot
// create a file in cgroupfs. That broke migration's snapshot widen, so a
// graceful drain never emptied a node and no rolling rollout could finish.
//
// adr: 205
// spec: §11
func TestEnsureSubtreeControllers_EnablesMissingControllers(t *testing.T) {
	const available = "cpuset cpu io memory pids\n"
	for _, c := range perInstanceControllers {
		if got := enableAttempt(t, available, "", c); got != "+"+c {
			t.Errorf("controller %q: wrote %q, want %q", c, got, "+"+c)
		}
	}
	// memory is the load-bearing one: §11's per-VM fence cannot exist without
	// it, so assert it explicitly rather than relying on the loop's coverage.
	if got := enableAttempt(t, available, "", "memory"); got != "+memory" {
		t.Fatalf("memory was not enabled: wrote %q", got)
	}
}

// TestEnsureSubtreeControllers_SkipsUnavailableAndAlreadyEnabled pins that the
// helper never writes a controller the parent cannot delegate. The kernel
// applies a multi-token body atomically, so one unsupported token would reject
// the whole write and leave the usable controllers disabled — which is why the
// helper enables one controller per write.
//
// adr: 205
// spec: §11
func TestEnsureSubtreeControllers_SkipsUnavailableAndAlreadyEnabled(t *testing.T) {
	const available = "cpu memory\n"
	if got := enableAttempt(t, available, "memory\n", "memory"); got != "" {
		t.Errorf("re-enabled an already-on controller: wrote %q", got)
	}
	for _, absent := range []string{"pids", "io"} {
		if got := enableAttempt(t, available, "memory\n", absent); got != "" {
			t.Errorf("enabled %q, which the parent does not have available: wrote %q", absent, got)
		}
	}
	if got := enableAttempt(t, available, "memory\n", "cpu"); got != "+cpu" {
		t.Errorf("did not enable cpu, which was available and off: wrote %q", got)
	}
}

// TestEnsureSubtreeControllers_NonCgroupParentIsNotAnError keeps the pure-Go
// test tier and the Lima shim working, where cgroupRoot is a plain directory.
// The caller's own memory.max write still fails loudly on a real host.
//
// adr: 205
// spec: §11
func TestEnsureSubtreeControllers_NonCgroupParentIsNotAnError(t *testing.T) {
	if err := ensureSubtreeControllers(t.TempDir(), perInstanceControllers); err != nil {
		t.Fatalf("plain directory must not be an error, got %v", err)
	}
}

func writeCgroupFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCgroupFile(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
