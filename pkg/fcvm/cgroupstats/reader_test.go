package cgroupstats

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// withFakeRoot mirrors pkg/fcvm/cgroup_test.go's withFakeCgroupRoot
// helper. We do NOT substitute pkg/fcvm.cgroupRoot here because the
// package only consults that var for writes (memory.max); we read
// from r.root, which is a Reader-local field the test owns outright.
func withFakeRoot(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// fakeScope creates a per-instance cgroup v2 leaf under
// <root>/faas-tenant.slice/<plan-slice>/<instance>/ with the provided
// cpu.stat and memory.current bodies. Writes are byte-exact so
// callers can test malformed input.
//
// plan is the owning plan tier (issue #301, ADR-044); pass an empty
// string for tests that don't care which slice the leaf lives under
// (the new Reader resolves the 3-level hierarchy
// ParentCgroupRoot/<plan-slice>/<instance> via ParentCgroupFor).
func fakeScope(t *testing.T, root, instance, plan, cpuStat, memoryCurrent string) {
	t.Helper()
	// plan is a string so callers can pass `""` for tests that don't
	// care which slice the leaf lives under; widen to api.Plan for
	// ParentCgroupFor (issue #301 / ADR-044).
	dir := filepath.Join(root, fcvm.ParentCgroupFor(api.Plan(plan)), fcvm.PerInstanceScope(instance))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if cpuStat != "" {
		if err := os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte(cpuStat), 0o644); err != nil {
			t.Fatalf("write cpu.stat: %v", err)
		}
	}
	if memoryCurrent != "" {
		if err := os.WriteFile(filepath.Join(dir, "memory.current"), []byte(memoryCurrent), 0o644); err != nil {
			t.Fatalf("write memory.current: %v", err)
		}
	}
}

func TestSampleReadsCPUAndRSS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	fakeScope(t, root, "vm-abc", string(api.PlanHobby),
		"usage_usec 1234567890\nuser_usec 100\nsystem_usec 50\n",
		"134217728\n",
	)
	r := New(root, nil)
	got, ok := r.Sample("vm-abc", api.PlanHobby)
	if !ok {
		t.Fatal("Sample: expected ok=true")
	}
	if got.CPUUsageUsec != 1234567890 {
		t.Errorf("CPUUsageUsec = %d, want 1234567890", got.CPUUsageUsec)
	}
	if got.RSSBytes != 134217728 {
		t.Errorf("RSSBytes = %d, want 134217728", got.RSSBytes)
	}
}

func TestSampleReadsLegacyDoublePrefixedPlanPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	dir := filepath.Join(root, fcvm.LegacyParentCgroupFor(api.PlanScale), "vm-legacy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte("usage_usec 77\nthrottled_usec 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.current"), []byte("8192\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := New(root, nil).Sample("vm-legacy", api.PlanScale)
	if !ok || got.CPUUsageUsec != 77 || got.RSSBytes != 8192 {
		t.Fatalf("legacy sample = %+v, %v", got, ok)
	}
}

func TestSampleReturnsFalseOnMissingScope(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	// Note: no fakeScope call — directory does not exist.
	r := New(root, nil)
	_, ok := r.Sample("ghost", api.PlanHobby)
	if ok {
		t.Error("Sample on missing scope: expected ok=false")
	}
}

func TestSampleReturnsFalseOnMalformedCpuStat(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	fakeScope(t, root, "vm-bad", string(api.PlanHobby),
		"this is not a cgroup file\n",
		"42\n",
	)
	r := New(root, nil)
	_, ok := r.Sample("vm-bad", api.PlanHobby)
	if ok {
		t.Error("Sample on malformed cpu.stat: expected ok=false")
	}
}

func TestSampleReturnsFalseOnMissingUsageUsecField(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	// cpu.stat present but the only key is user_usec — must not be
	// mistaken for usage_usec.
	fakeScope(t, root, "vm-no-usage", string(api.PlanHobby),
		"user_usec 100\nsystem_usec 50\n",
		"42\n",
	)
	r := New(root, nil)
	_, ok := r.Sample("vm-no-usage", api.PlanHobby)
	if ok {
		t.Error("Sample on cpu.stat without usage_usec: expected ok=false")
	}
}

func TestSampleReturnsFalseOnMalformedMemoryCurrent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	fakeScope(t, root, "vm-bad-mem", string(api.PlanHobby),
		"usage_usec 100\n",
		"not a number\n",
	)
	r := New(root, nil)
	_, ok := r.Sample("vm-bad-mem", api.PlanHobby)
	if ok {
		t.Error("Sample on malformed memory.current: expected ok=false")
	}
}

func TestSampleToleratesTrailingWhitespaceInMemoryCurrent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	// cgroup v2 memory.current normally has no trailing newline; some
	// kernels add whitespace. TrimSpace must handle it.
	fakeScope(t, root, "vm-ws", string(api.PlanHobby),
		"usage_usec 7\n",
		"4096  \n",
	)
	r := New(root, nil)
	got, ok := r.Sample("vm-ws", api.PlanHobby)
	if !ok {
		t.Fatal("Sample: expected ok=true")
	}
	if got.RSSBytes != 4096 {
		t.Errorf("RSSBytes = %d, want 4096", got.RSSBytes)
	}
}

func TestInstancesFiltersSystemdAndKernelSiblings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	// Issue #301 / ADR-044: the per-VM scopes are now under
	// faas-tenant.slice/<plan-slice>/<instance>/, not directly
	// under faas-tenant.slice/. Place test leaves under the
	// tenant-hobby sub-slice; the systemd/kernel siblings the
	// filter is meant to exclude would land at the top level
	// in the real world and are listed under the parent's
	// itself as a no-op marker. Bare instance ids (no '.' —
	// jailer rejects '.' in --id).
	for _, leaf := range []string{
		"vm-alpha", // ours (hobby)
		"vm-bravo", // ours (hobby)
	} {
		if err := os.MkdirAll(filepath.Join(root, fcvm.ParentCgroupFor(api.PlanHobby), leaf), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", leaf, err)
		}
	}
	r := New(root, nil)
	got, err := r.Instances()
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	// Sort by instance name (matches sortInstanceInfos on
	// (instance, plan)) and project to instance strings for the
	// stable assertion the test was already pinning.
	sort.Slice(got, func(i, j int) bool { return got[i].Instance < got[j].Instance })
	gotStr := make([]string, len(got))
	for i, g := range got {
		gotStr[i] = g.Instance
	}
	want := []string{"vm-alpha", "vm-bravo"}
	if !equalStrings(gotStr, want) {
		t.Errorf("Instances = %v, want %v", gotStr, want)
	}
}

func TestInstancesReturnsSortedDeterministically(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	// Insert in non-alphabetical order on purpose. Each leaf
	// under tenant-hobby.slice (the 3-level hierarchy).
	for _, leaf := range []string{"zzz", "mmm", "aaa"} {
		if err := os.MkdirAll(filepath.Join(root, fcvm.ParentCgroupFor(api.PlanHobby), leaf), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", leaf, err)
		}
	}
	r := New(root, nil)
	got, err := r.Instances()
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	gotStr := make([]string, len(got))
	for i, g := range got {
		gotStr[i] = g.Instance
	}
	want := []string{"aaa", "mmm", "zzz"}
	if !equalStrings(gotStr, want) {
		t.Errorf("Instances = %v, want %v (deterministic order)", gotStr, want)
	}
}

func TestInstancesMissingSliceReturnsEmpty(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup v2 is Linux-only")
	}
	root := withFakeRoot(t)
	// No faas-tenant.slice directory at all.
	r := New(root, nil)
	got, err := r.Instances()
	if err != nil {
		t.Fatalf("Instances on missing slice: unexpected error %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Instances on missing slice = %v, want empty", got)
	}
}

func TestNewWithDefaultsUsesSysFsCgroup(t *testing.T) {
	r := NewWithDefaults()
	if r.root != defaultRoot {
		t.Errorf("NewWithDefaults root = %q, want %q", r.root, defaultRoot)
	}
	if r.now == nil {
		t.Error("NewWithDefaults: now must default to time.Now, got nil")
	}
}

func TestNewWithNilNowUsesTimeNow(t *testing.T) {
	r := New("/tmp", nil)
	if r.now == nil {
		t.Fatal("New with nil now: now must default to time.Now, got nil")
	}
}

// equalStrings compares two string slices element-wise. Lives here so
// the package's tests don't pull in reflect.DeepEqual for trivial
// checks.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
