package fcvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// withFakeCgroupRoot redirects the package-level cgroupRoot var to
// t.TempDir() for the test and restores it on cleanup. Mirrors the
// TestMain blanket override in manager_test.go but is per-test (the
// blanket override is for cases that don't care about cgroups; these
// tests do care and want a clean tree each time).
func withFakeCgroupRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	saved := cgroupRoot
	cgroupRoot = dir
	t.Cleanup(func() { cgroupRoot = saved })
	return dir
}

func TestWriteMemoryMaxWritesBytesPlusOverhead(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	inst := "foo"
	if err := os.MkdirAll(filepath.Join(dir, "faas-tenant.slice", PerInstanceScope(inst)), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := writeMemoryMax(inst, 128); err != nil {
		t.Fatalf("writeMemoryMax: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "faas-tenant.slice", PerInstanceScope(inst), "memory.max"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// 128 plan MB + 8 MB PerVMOverheadMB = 136 MiB = 136 << 20 bytes.
	const want = (128 + 8) << 20
	got := strings.TrimSpace(string(body))
	if got != itoa(want) {
		t.Errorf("memory.max = %q, want %d", got, want)
	}
}

func TestWriteMemoryMaxMissingScopeFailsClean(t *testing.T) {
	withFakeCgroupRoot(t) // does NOT create the scope dir
	err := writeMemoryMax("bar", 128)
	if err == nil {
		t.Fatal("expected error when scope is missing")
	}
	if !strings.Contains(err.Error(), "scope") || !strings.Contains(err.Error(), "bar") {
		t.Errorf("error %q must name the missing scope and the instance id", err.Error())
	}
	if !strings.Contains(err.Error(), "faas-tenant.slice/"+PerInstanceScope("bar")) {
		t.Errorf("error %q must include the full scope path", err.Error())
	}
}

func TestWriteMemoryMaxRejectsNonPositivePlan(t *testing.T) {
	withFakeCgroupRoot(t)
	for _, planMB := range []int{0, -1, -1024} {
		err := writeMemoryMax("foo", planMB)
		if err == nil {
			t.Errorf("writeMemoryMax(foo, %d): expected error, got nil", planMB)
			continue
		}
		if !strings.Contains(err.Error(), "planMB") {
			t.Errorf("writeMemoryMax(foo, %d): error %q must mention planMB", planMB, err.Error())
		}
	}
}

func TestWriteMemoryMaxAppendsNewline(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	inst := "baz"
	if err := os.MkdirAll(filepath.Join(dir, "faas-tenant.slice", PerInstanceScope(inst)), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := writeMemoryMax(inst, 256); err != nil {
		t.Fatalf("writeMemoryMax: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "faas-tenant.slice", PerInstanceScope(inst), "memory.max"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(body) == 0 || body[len(body)-1] != '\n' {
		t.Errorf("memory.max body %q must end with newline (kernel parser expectation)", body)
	}
}

func TestWriteAppCgroupUsesConfiguredCPU(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	inst := "configured-cpu"
	scope := filepath.Join(dir, ParentCgroupFor(api.PlanPro), PerInstanceScope(inst))
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := writeAppCgroup(inst, api.PlanPro, 256, 500); err != nil {
		t.Fatalf("writeAppCgroup: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(scope, "cpu.max"))
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if got, want := string(body), "250000 500000\n"; got != want {
		t.Fatalf("cpu.max = %q, want %q", got, want)
	}
}

func TestWidenSnapshotMemoryCgroupRestoresOrdinaryFence(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	inst := "snapshot-headroom"
	scope := filepath.Join(dir, ParentCgroupFor(api.PlanScale), PerInstanceScope(inst))
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	l := Lease{Instance: inst, Plan: api.PlanScale, MemoryMaxMiB: 1024}
	if err := writeMemoryMaxAt(scope, api.BillableRAMMB(l.MemoryMaxMiB)); err != nil {
		t.Fatalf("write ordinary fence: %v", err)
	}

	restore, err := widenSnapshotMemoryCgroup(l)
	if err != nil {
		t.Fatalf("widenSnapshotMemoryCgroup: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(scope, "memory.max"))
	if err != nil {
		t.Fatalf("read widened fence: %v", err)
	}
	if got, want := strings.TrimSpace(string(body)), itoa(api.SnapshotMemoryMaxMB(l.MemoryMaxMiB)<<20); got != want {
		t.Errorf("widened memory.max = %q, want %s", got, want)
	}
	if err := restore(); err != nil {
		t.Fatalf("restore snapshot fence: %v", err)
	}
	body, err = os.ReadFile(filepath.Join(scope, "memory.max"))
	if err != nil {
		t.Fatalf("read restored fence: %v", err)
	}
	if got, want := strings.TrimSpace(string(body)), itoa(api.BillableRAMMB(l.MemoryMaxMiB)<<20); got != want {
		t.Errorf("restored memory.max = %q, want %s", got, want)
	}
}

func TestWidenSnapshotMemoryCgroupSkipsBuilders(t *testing.T) {
	withFakeCgroupRoot(t)
	restore, err := widenSnapshotMemoryCgroup(Lease{Instance: "builder", IsBuilder: true, MemoryMaxMiB: api.BuildVMRAMMB})
	if err != nil {
		t.Fatalf("builder snapshot headroom: %v", err)
	}
	if err := restore(); err != nil {
		t.Fatalf("builder snapshot restore: %v", err)
	}
}

// itoa is a tiny strconv alternative — avoids importing strconv just
// for one assertion; the package's other tests don't need it.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// TestWriteWorkloadCgroup (issue #463 / ADR-069 / PR-B) pins the
// per-workload child scope contract: parent scope is created,
// child scope mkdir'd under it, memory.max = workload ram_mb
// (in bytes, no +8 MB overhead — the overhead lives on the parent
// scope). Idempotent on a second call (same value).
func TestWriteWorkloadCgroup(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	parent := filepath.Join(dir, "faas-tenant.slice", "test-tenant-hobby", "i-1")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("setup parent: %v", err)
	}
	if err := writeWorkloadCgroup(parent, "main", 256); err != nil {
		t.Fatalf("writeWorkloadCgroup main: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(parent, "main", "memory.max"))
	if err != nil {
		t.Fatalf("read main/memory.max: %v", err)
	}
	// 256 MB = 256 << 20 bytes (no +8 MB overhead on child).
	const want = 256 << 20
	if got := strings.TrimSpace(string(body)); got != itoa(want) {
		t.Errorf("main/memory.max = %q, want %d", got, want)
	}
	// Idempotent: a second call with the same value is a no-op.
	if err := writeWorkloadCgroup(parent, "main", 256); err != nil {
		t.Errorf("writeWorkloadCgroup main (idempotent): %v", err)
	}
}

// TestWriteWorkloadCgroupMultiSidecars covers the AC #4 /
// ADR-069 §"Downstream" case: multiple sidecar children share
// the same parent scope with independent memory.max values.
// The kernel evaluates the innermost effective memory.max, so
// the sidecar cap is enforced independently of the main cap.
func TestWriteWorkloadCgroupMultiSidecars(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	parent := filepath.Join(dir, "faas-tenant.slice", "test-tenant-pro", "i-9")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("setup parent: %v", err)
	}
	if err := writeWorkloadCgroup(parent, "main", 512); err != nil {
		t.Fatalf("writeWorkloadCgroup main: %v", err)
	}
	if err := writeWorkloadCgroup(parent, "metrics", 64); err != nil {
		t.Fatalf("writeWorkloadCgroup metrics: %v", err)
	}
	if err := writeWorkloadCgroup(parent, "logger", 32); err != nil {
		t.Fatalf("writeWorkloadCgroup logger: %v", err)
	}
	for _, c := range []struct {
		name string
		mb   int
		want int
	}{
		{"main", 512, 512 << 20},
		{"metrics", 64, 64 << 20},
		{"logger", 32, 32 << 20},
	} {
		body, err := os.ReadFile(filepath.Join(parent, c.name, "memory.max"))
		if err != nil {
			t.Fatalf("read %s/memory.max: %v", c.name, err)
		}
		if got := strings.TrimSpace(string(body)); got != itoa(c.want) {
			t.Errorf("%s/memory.max = %q, want %d", c.name, got, c.want)
		}
	}
}

func TestWriteWorkloadCgroupCPUOnly(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	parent := filepath.Join(dir, "faas-tenant.slice", "test-tenant-pro", "i-cpu")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("setup parent: %v", err)
	}
	if err := writeWorkloadCgroup(parent, "metrics", 0, 250); err != nil {
		t.Fatalf("writeWorkloadCgroup cpu-only: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "metrics", "memory.max")); !os.IsNotExist(err) {
		t.Fatalf("cpu-only workload should not write memory.max, err=%v", err)
	}
	body, err := os.ReadFile(filepath.Join(parent, "metrics", "cpu.max"))
	if err != nil {
		t.Fatalf("read metrics/cpu.max: %v", err)
	}
	if got, want := strings.TrimSpace(string(body)), "25000 100000"; got != want {
		t.Fatalf("metrics/cpu.max = %q, want %q", got, want)
	}
}

// TestWriteWorkloadCgroupRejectsPathTraversal pins the security
// scan: a workload name containing "/" or ".." must not be
// allowed to escape the parent scope. The reject happens BEFORE
// any mkdir so the parent stays clean.
func TestWriteWorkloadCgroupRejectsPathTraversal(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	parent := filepath.Join(dir, "faas-tenant.slice", "test-tenant-hobby", "i-1")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	for _, name := range []string{"../escape", "evil/path", ".", "..", "ok\\evil"} {
		err := writeWorkloadCgroup(parent, name, 64)
		if err == nil {
			t.Errorf("writeWorkloadCgroup(%q): expected error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), "workload") {
			t.Errorf("writeWorkloadCgroup(%q): error %q must mention workload", name, err.Error())
		}
	}
	// Parent must still be clean (no child dirs created).
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("parent should have no children after rejected writes, got: %v", names)
	}
}

// TestWriteWorkloadCgroupRejectsEmpty pins the empty-name guard.
func TestWriteWorkloadCgroupRejectsEmpty(t *testing.T) {
	withFakeCgroupRoot(t)
	if err := writeWorkloadCgroup("/tmp", "", 64); err == nil {
		t.Fatal("expected error for empty workload name")
	}
}

// TestWriteWorkloadCgroupRejectsNonPositiveRAM pins the positive-
// ram guard. The child scope is meaningless with 0 MB or negative
// RAM (the kernel would interpret 0 as "no limit" and the parent
// scope's memory.max would be the only effective cap).
func TestWriteWorkloadCgroupRejectsNonPositiveRAM(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	parent := filepath.Join(dir, "p")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	for _, ramMB := range []int{0, -1, -1024} {
		if err := writeWorkloadCgroup(parent, "main", ramMB); err == nil {
			t.Errorf("writeWorkloadCgroup(main, %d): expected error", ramMB)
		}
	}
}

// TestRemoveWorkloadCgroupsMirrorsWrite pins the teardown
// contract: every workload we wrote in writeWorkloadCgroup must
// be removed by removeWorkloadCgroups. The function is
// best-effort; missing children are silently skipped (the
// kernel may have already cascade-removed them).
func TestRemoveWorkloadCgroupsMirrorsWrite(t *testing.T) {
	dir := withFakeCgroupRoot(t)
	parent := filepath.Join(dir, "faas-tenant.slice", "test-tenant-hobby", "i-1")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	for _, name := range []string{"main", "metrics", "logger"} {
		if err := writeWorkloadCgroup(parent, name, 64); err != nil {
			t.Fatalf("writeWorkloadCgroup %s: %v", name, err)
		}
	}
	removed := removeWorkloadCgroups(parent, []string{"main", "metrics", "logger"})
	if len(removed) != 3 {
		t.Errorf("removeWorkloadCgroups returned %d, want 3", len(removed))
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("parent should be empty after teardown, got: %v", names)
	}
	// Idempotent: a second call with the same names is a no-op
	// (children already gone, kernel cascades cleanly).
	removed = removeWorkloadCgroups(parent, []string{"main", "metrics", "logger"})
	if len(removed) != 0 {
		t.Errorf("second remove should be no-op, got %d", len(removed))
	}
}
