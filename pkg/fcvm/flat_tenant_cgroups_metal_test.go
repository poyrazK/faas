//go:build linux && metal

// adr: 20261109172421 step 6.5 — with flat_tenant_cgroups on, a new tenant VM
// launches directly under faas-tenant.slice with its size's cpu.weight and
// its memory fence; vmmd freezes, thaws and tears it down there and leaves
// nothing behind.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

func TestMetalFlatTenantCgroups(t *testing.T) {
	kernel, base, layer := metalImages(t)
	if os.Geteuid() != 0 {
		t.Skip("requires root and dedicated Linux KVM")
	}
	t.Cleanup(func() { leakcheck.AssertZero(t) })
	ctx := context.Background()
	m := newMetalManager(t, kernel).WithFlatTenantCgroups(true)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	const id = "flat-metal-vm"
	const memMiB = 512
	inst, err := m.ColdBoot(ctx, ColdBootRequest{Instance: id, Plan: api.PlanPro, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: memMiB})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })

	pid, ok := m.InstancePID(id)
	if !ok {
		t.Fatal("no guest pid")
	}
	scope := filepath.Join("/sys/fs/cgroup", CgroupMountRoot, PerInstanceScope(id))
	cgroup, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(cgroup)), "0::/"+filepath.Join(CgroupMountRoot, PerInstanceScope(id)); got != want {
		t.Fatalf("guest runs in %q, want the flat scope %q", got, want)
	}
	readFile := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(scope, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return strings.TrimSpace(string(b))
	}
	if got, want := readFile("cpu.weight"), strconv.Itoa(api.SizeCPUWeight(memMiB)); got != want {
		t.Fatalf("cpu.weight = %s, want %s (the size's weight)", got, want)
	}
	if got, want := readFile("memory.max"), strconv.Itoa(api.AppVMMemoryMaxMB(memMiB)<<20); got != want {
		t.Fatalf("memory.max = %s, want %s (plan + overhead)", got, want)
	}
	url := "http://" + inst.Lease.HostIP.String() + ":8080/"
	serve := func(what string) {
		t.Helper()
		resp, err := firstByte(url, 10*time.Second)
		if err != nil {
			t.Fatalf("%s guest does not serve: %v", what, err)
		}
		_ = resp.Body.Close()
	}
	serve("flat")

	if _, err := m.Freeze(ctx, id, FreezeOptions{}); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if frozen, _ := m.FrozenState(id); !frozen {
		t.Fatal("freeze did not report the guest frozen")
	}
	if err := m.Thaw(ctx, id); err != nil {
		t.Fatalf("thaw: %v", err)
	}
	serve("thawed flat")

	if err := m.Destroy(ctx, id); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(scope); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("flat scope left after destroy: %v", err)
	}
}
