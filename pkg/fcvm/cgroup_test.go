package fcvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.MkdirAll(filepath.Join(dir, "faas-tenant.slice", "vm-"+inst+".scope"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := writeMemoryMax(inst, 128); err != nil {
		t.Fatalf("writeMemoryMax: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "faas-tenant.slice", "vm-"+inst+".scope", "memory.max"))
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
	if !strings.Contains(err.Error(), "faas-tenant.slice/vm-bar.scope") {
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
	if err := os.MkdirAll(filepath.Join(dir, "faas-tenant.slice", "vm-"+inst+".scope"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := writeMemoryMax(inst, 256); err != nil {
		t.Fatalf("writeMemoryMax: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "faas-tenant.slice", "vm-"+inst+".scope", "memory.max"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(body) == 0 || body[len(body)-1] != '\n' {
		t.Errorf("memory.max body %q must end with newline (kernel parser expectation)", body)
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
