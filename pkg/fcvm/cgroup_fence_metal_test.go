//go:build metal && linux

// spec: §11 — the per-VM fence survives a controller reset on real cgroupfs.
package fcvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestMetalRepairCgroupFencesAfterControllerReset reproduces, on a real
// cgroup v2 hierarchy, what a systemd daemon-reload does to a VM parent slice
// without a fence pin: the parent's controllers are disabled, the scope loses
// its fence files, and re-enabling them brings back kernel defaults. The
// repair must restore the recorded fence.
func TestMetalRepairCgroupFencesAfterControllerReset(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create cgroups")
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Skip("needs a cgroup v2 mount")
	}
	root := filepath.Join("/sys/fs/cgroup", fmt.Sprintf("fence-metal-%d", os.Getpid()))
	l := Lease{Instance: "fence-metal-vm", Plan: api.PlanScale, MemoryMaxMiB: 128}
	parent := filepath.Join(root, vmCgroupParent(l))
	scope := filepath.Join(parent, PerInstanceScope(l.Instance))

	// Every level from the real root down to the parent delegates the
	// controllers, as systemd does for slices whose child units need them.
	levels := []string{root}
	for dir := root; dir != parent; {
		rel, _ := filepath.Rel(dir, parent)
		dir = filepath.Join(dir, strings.Split(rel, string(filepath.Separator))[0])
		levels = append(levels, dir)
	}
	t.Cleanup(func() {
		_ = os.Remove(scope)
		for i := len(levels) - 1; i >= 0; i-- {
			_ = os.Remove(levels[i])
		}
	})
	enable := func(dir string) {
		t.Helper()
		for _, c := range perInstanceControllers {
			if err := os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"), []byte("+"+c), 0o644); err != nil {
				t.Fatalf("enable %s in %s: %v", c, dir, err)
			}
		}
	}
	enable("/sys/fs/cgroup")
	for _, dir := range levels {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		enable(dir)
	}
	if err := os.Mkdir(scope, 0o755); err != nil {
		t.Fatalf("mkdir scope: %v", err)
	}
	withCgroupRootAt(t, root)

	// jailer sets cpu.weight at scope creation; vmmd writes the rest.
	writeCgroupFile(t, scope, "cpu.weight", itoa(vmCPUWeight(l.Plan, false)))
	v := &JailerVMM{}
	if err := v.applyPreBootCgroupFence(l, nil); err != nil {
		t.Fatalf("applyPreBootCgroupFence: %v", err)
	}
	wantCPU, err := appCPUMax(l.Plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"memory.max": memoryMaxValue(api.BillableRAMMB(l.MemoryMaxMiB)),
		"cpu.weight": itoa(api.PlanScale.CPUWeight()),
		"cpu.max":    wantCPU,
	}

	// systemd's reload: the parent's subtree_control becomes empty.
	for _, c := range perInstanceControllers {
		if err := os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("-"+c), 0o644); err != nil {
			t.Fatalf("disable %s: %v", c, err)
		}
	}
	if _, err := os.Stat(filepath.Join(scope, "memory.max")); !os.IsNotExist(err) {
		t.Fatalf("memory.max still present after disabling the controller (err=%v)", err)
	}

	rep := v.RepairCgroupFences(context.Background())
	if rep.Repaired != 1 || rep.Failed != 0 {
		t.Fatalf("report = %+v, want one repair", rep)
	}
	for name, value := range want {
		if got := readFenceFile(t, scope, name); got != value {
			t.Errorf("%s = %q after repair, want %q", name, got, value)
		}
	}
}
