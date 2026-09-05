// config_vmm_mega4_test.go — Coverage Mega-PR #4 cluster 5:
// fill pkg/fcvm coverage on the pure helpers in config.go +
// the small constructor / setter / path-pure helpers in vmm.go
// that don't require Firecracker.
//
// Targets:
//   - BuildColdBootConfig (legacy + PR-B multi-workload paths)
//   - NewVsockDevice
//   - ColdBootSpec.Validate (table across all branches)
//   - ParentCgroupFor, GuestVsockCID, PerInstanceScope
//   - JailerCommand (incl. builder / empty-plan fallback)
//   - resolveFCChrootName (eval-symlinks path)
//   - chrootRoot / socketPath / vsockUDSSock (path builders)
//   - NewJailerVMM constructor (default readyTimeout + 0-disables)
//   - WithStorage / WithEvents / WithSlowSubscriberCallback
//   - LogRing / ringFor / registerRing / unregisterRing
//   - exportMax / destroyWaitFor
//
// Whitebox `package fcvm`. No Firecracker binary required.

package fcvm

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- BuildColdBootConfig ---------------------------------------

func TestBuildColdBootConfig_LegacyPath_Mega4(t *testing.T) {
	t.Parallel()
	spec := ColdBootSpec{
		KernelKey:  "k-1",
		BaseKey:    "b-1",
		LayerKey:   "l-1",
		Tap:        "tap0",
		VcpuCount:  2,
		MemSizeMiB: 256,
	}
	cfg := BuildColdBootConfig(spec, 7)
	if len(cfg.Drives) != 2 {
		t.Fatalf("drives len = %d, want 2 (base + layer)", len(cfg.Drives))
	}
	if cfg.Drives[0].DriveID != DriveBase || !cfg.Drives[0].IsReadOnly || !cfg.Drives[0].IsRootDevice {
		t.Errorf("base drive: %+v", cfg.Drives[0])
	}
	if cfg.Drives[1].DriveID != DriveLayer || cfg.Drives[1].IsReadOnly || cfg.Drives[1].IsRootDevice {
		t.Errorf("layer drive: %+v", cfg.Drives[1])
	}
	if cfg.VsockDevice == nil || cfg.VsockDevice.GuestCID != VsockCIDBase+7 {
		t.Errorf("vsock: %+v", cfg.VsockDevice)
	}
	if len(cfg.NetworkInterfaces) != 1 || cfg.NetworkInterfaces[0].HostDevName != "tap0" {
		t.Errorf("net: %+v", cfg.NetworkInterfaces)
	}
}

func TestBuildColdBootConfig_WorkloadsPath_Mega4(t *testing.T) {
	t.Parallel()
	spec := ColdBootSpec{
		KernelKey: "k-1",
		BaseKey:   "b-1",
		Workloads: []WorkloadSpec{
			{Name: "main", StorageKey: "main-key"},
			{Name: "logs", StorageKey: "logs-key"},
			{Name: "envoy", StorageKey: "envoy-key"},
		},
		Tap:        "tap1",
		VcpuCount:  1,
		MemSizeMiB: 128,
	}
	cfg := BuildColdBootConfig(spec, 3)
	// 1 base + 3 workloads = 4 drives.
	if len(cfg.Drives) != 4 {
		t.Fatalf("drives len = %d, want 4", len(cfg.Drives))
	}
	// Workloads[0] is RW; sidecars RO.
	if cfg.Drives[1].IsReadOnly {
		t.Error("main workload should be RW")
	}
	for i := 2; i < 4; i++ {
		if !cfg.Drives[i].IsReadOnly {
			t.Errorf("sidecar %d should be RO", i-1)
		}
	}
}

func TestBuildColdBootConfig_WorkloadHasExplicitDriveID_Mega4(t *testing.T) {
	t.Parallel()
	spec := ColdBootSpec{
		KernelKey: "k", BaseKey: "b",
		Workloads: []WorkloadSpec{
			{Name: "main", StorageKey: "k1", DriveID: "custom-main"},
			{Name: "logs", StorageKey: "k2"},
		},
	}
	cfg := BuildColdBootConfig(spec, 0)
	if cfg.Drives[1].DriveID != "custom-main" {
		t.Errorf("explicit DriveID not preserved: %+v", cfg.Drives[1])
	}
}

// --- NewVsockDevice ---------------------------------------------

func TestNewVsockDevice_Mega4(t *testing.T) {
	t.Parallel()
	for _, slot := range []int{0, 1, 100, 4095} {
		d := NewVsockDevice(slot)
		if d.ID != VsockDeviceID {
			t.Errorf("slot %d: ID = %q", slot, d.ID)
		}
		if d.GuestCID != GuestVsockCID(slot) {
			t.Errorf("slot %d: CID = %d", slot, d.GuestCID)
		}
		if d.UDSSocket != VsockUDSSocketName {
			t.Errorf("slot %d: UDS = %q", slot, d.UDSSocket)
		}
	}
}

// --- ColdBootSpec.Validate --------------------------------------

func TestColdBootSpecValidate_Mega4(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    ColdBootSpec
		wantErr string
	}{
		{
			name:    "empty kernel",
			spec:    ColdBootSpec{BaseKey: "b", LayerKey: "l", VcpuCount: 1, MemSizeMiB: 128, Tap: "tap0"},
			wantErr: "kernel key",
		},
		{
			name:    "empty base",
			spec:    ColdBootSpec{KernelKey: "k", LayerKey: "l", VcpuCount: 1, MemSizeMiB: 128, Tap: "tap0"},
			wantErr: "base rootfs",
		},
		{
			name:    "empty layer + no workloads",
			spec:    ColdBootSpec{KernelKey: "k", BaseKey: "b", VcpuCount: 1, MemSizeMiB: 128, Tap: "tap0"},
			wantErr: "app-layer",
		},
		{
			name: "layer + workloads dual-spec rejected",
			spec: ColdBootSpec{
				KernelKey: "k", BaseKey: "b", LayerKey: "l", VcpuCount: 1, MemSizeMiB: 128, Tap: "tap0",
				Workloads: []WorkloadSpec{{Name: "main", StorageKey: "m"}},
			},
			wantErr: "LayerKey",
		},
		{
			name: "happy legacy",
			spec: ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", VcpuCount: 1, MemSizeMiB: 128, Tap: "tap0"},
		},
		{
			name: "happy workloads",
			spec: ColdBootSpec{
				KernelKey: "k", BaseKey: "b", VcpuCount: 1, MemSizeMiB: 128, Tap: "tap0",
				Workloads: []WorkloadSpec{{Name: "main", StorageKey: "m"}},
			},
		},
		{
			name:    "zero vcpu_count rejected",
			spec:    ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", Tap: "tap0"},
			wantErr: "vcpu_count",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := c.spec.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want err containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want contains %q", err, c.wantErr)
			}
		})
	}
}

// --- ParentCgroupFor / GuestVsockCID / PerInstanceScope ---------

func TestParentCgroupFor_Mega4(t *testing.T) {
	t.Parallel()
	for _, p := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		got := ParentCgroupFor(p)
		if got == "" {
			t.Errorf("plan %s: empty", p)
		}
		if !strings.HasPrefix(got, CgroupMountRoot) {
			t.Errorf("plan %s: %q missing prefix %q", p, got, CgroupMountRoot)
		}
		want := CgroupMountRoot + "/faas-" + p.SliceName() + ".slice"
		if got != want {
			t.Errorf("plan %s: %q, want %q", p, got, want)
		}
	}
	// Empty plan falls back to the neutral default.
	if got := ParentCgroupFor(""); got != defaultParentCgroup {
		t.Errorf("empty plan: %q, want default %q", got, defaultParentCgroup)
	}
}

func TestGuestVsockCID_Mega4(t *testing.T) {
	t.Parallel()
	// CIDs are unique across the slot range and skip the reserved kernel range.
	seen := map[uint32]int{}
	for slot := 0; slot < 32; slot++ {
		cid := GuestVsockCID(slot)
		if cid <= HostVsockCID {
			t.Errorf("slot %d: cid %d not above host reserved", slot, cid)
		}
		if cid != VsockCIDBase+uint32(slot) {
			t.Errorf("slot %d: cid %d, want %d", slot, cid, VsockCIDBase+uint32(slot))
		}
		seen[cid]++
	}
	if len(seen) != 32 {
		t.Errorf("duplicate CIDs: %d unique, want 32", len(seen))
	}
}

func TestPerInstanceScope_Mega4(t *testing.T) {
	t.Parallel()
	if got := PerInstanceScope("inst-1"); got != "inst-1" {
		t.Errorf("identity not preserved: %q", got)
	}
	if got := PerInstanceScope(""); got != "" {
		t.Errorf("empty passthrough: %q", got)
	}
}

// --- JailerCommand ---------------------------------------------

func TestJailerCommand_Mega4(t *testing.T) {
	t.Parallel()

	t.Run("empty plan uses kernel default 100", func(t *testing.T) {
		t.Parallel()
		args := JailerCommand(JailerSpec{
			Instance: "i-1", UID: 100, GID: 100, Netns: "fc-i-1", ExecFile: "",
		})
		// Empty plan: CPUWeight() returns 100 (kernel default
		// for unknown plans, per api.Plan.CPUWeight comment).
		if !containsKV(args, "cpu.weight=100") {
			t.Errorf("missing cpu.weight=100: %v", args)
		}
		// parent_cgroup falls back to faas.slice/faas-tenant.slice.
		if !containsArg(args, defaultParentCgroup) {
			t.Errorf("parent cgroup not default: %v", args)
		}
	})

	t.Run("plan-derived weight", func(t *testing.T) {
		t.Parallel()
		args := JailerCommand(JailerSpec{
			Instance: "i-1", UID: 1, GID: 1, Netns: "fc-i-1", Plan: api.PlanPro,
		})
		want := api.PlanPro.CPUWeight()
		if want <= 0 {
			t.Fatal("PlanPro.CPUWeight: non-positive test fixture")
		}
		if !containsKV(args, "cpu.weight="+itoa_Mega4(want)) {
			t.Errorf("missing cpu.weight=%d: %v", want, args)
		}
	})

	t.Run("builder overrides plan and weight", func(t *testing.T) {
		t.Parallel()
		args := JailerCommand(JailerSpec{
			Instance: "b-1", UID: 1, GID: 1, Netns: "fc-b-1",
			Plan: api.PlanPro, IsBuilder: true,
		})
		if !containsKV(args, "cpu.weight=256") {
			t.Errorf("builder weight not 256: %v", args)
		}
		if !containsArg(args, BuilderCgroupParent) {
			t.Errorf("builder parent cgroup: %v", args)
		}
	})

	t.Run("memory.max only when set", func(t *testing.T) {
		t.Parallel()
		without := JailerCommand(JailerSpec{Instance: "i", UID: 1, GID: 1, Netns: "n"})
		if containsKV(without, "memory.max=") {
			t.Errorf("zero memory: should not include memory.max: %v", without)
		}
		with := JailerCommand(JailerSpec{Instance: "i", UID: 1, GID: 1, Netns: "n", MemoryMaxBytes: 1024})
		if !containsKV(with, "memory.max=1024") {
			t.Errorf("memory.max=1024 missing: %v", with)
		}
	})

	t.Run("explicit exec-file overrides default", func(t *testing.T) {
		t.Parallel()
		args := JailerCommand(JailerSpec{
			Instance: "i", UID: 1, GID: 1, Netns: "n",
			ExecFile: "/custom/path/to/fc",
		})
		if !containsKV(args, "--exec-file") {
			t.Errorf("--exec-file missing: %v", args)
		}
	})
}

// --- resolveFCChrootName (binary-missing fallback) -------------

func TestResolveFCChrootName_Fallback_Mega4(t *testing.T) {
	t.Parallel()
	// The function exec.LookPath(FirecrackerBin). On any machine
	// without the binary, it falls back to FirecrackerBin literal.
	// Either path is acceptable; just confirm a non-empty value.
	got := resolveFCChrootName()
	if got == "" {
		t.Error("resolveFCChrootName: empty")
	}
}

// --- chrootRoot / socketPath / vsockUDSSock ---------------------

func TestJailerVMM_PathBuilders_Mega4(t *testing.T) {
	t.Parallel()
	v := NewJailerVMM("/srv/fc", 30*time.Second)
	instance := "abc-def_123"
	expectedRoot := filepath.Join("/srv/fc", v.fcName, instance, "root")
	if got := v.chrootRoot(instance); got != expectedRoot {
		t.Errorf("chrootRoot = %q, want %q", got, expectedRoot)
	}
	if got := v.socketPath(instance); got != filepath.Join(expectedRoot, APISockName) {
		t.Errorf("socketPath = %q", got)
	}
	vsockPath := v.vsockUDSSock(instance)
	if !strings.HasSuffix(vsockPath, VsockUDSSocketName) {
		t.Errorf("vsockUDSSock = %q (no VsockUDSSocketName suffix)", vsockPath)
	}
	if !strings.HasPrefix(vsockPath, expectedRoot) {
		t.Errorf("vsockUDSSock = %q (no chrootRoot prefix)", vsockPath)
	}
}

// --- NewJailerVMM constructor -----------------------------------

func TestNewJailerVMM_Defaults_Mega4(t *testing.T) {
	t.Parallel()
	v := NewJailerVMM("/srv/fc", 0)
	if v.chrootBase != "/srv/fc" {
		t.Errorf("chrootBase = %q", v.chrootBase)
	}
	if v.readyTimeout != 30*time.Second {
		t.Errorf("readyTimeout = %v, want 30s", v.readyTimeout)
	}
	if v.destroyWait != 10*time.Minute {
		t.Errorf("destroyWait = %v, want 10m", v.destroyWait)
	}
	if v.proc == nil || v.clients == nil || v.recs == nil || v.rings == nil {
		t.Error("constructor did not initialise maps")
	}
}

func TestNewJailerVMM_ExplicitTimeout_Mega4(t *testing.T) {
	t.Parallel()
	v := NewJailerVMM("/srv/fc", 5*time.Second)
	if v.readyTimeout != 5*time.Second {
		t.Errorf("readyTimeout = %v, want 5s", v.readyTimeout)
	}
}

// --- WithStorage / WithEvents / WithSlowSubscriberCallback -----

func TestJailerVMM_Setters_Mega4(t *testing.T) {
	t.Parallel()
	v := NewJailerVMM("/srv/fc", 0)
	if got := v.WithStorage(nil); got != v {
		t.Error("WithStorage(nil): not chainable")
	}
	if got := v.WithEvents(nil); got == nil {
		t.Error("WithEvents: nil receiver — should return VMM interface value")
	}
	// WithSlowSubscriberCallback returns *JailerVMM.
	called := false
	if got := v.WithSlowSubscriberCallback(func() { called = true }); got != v {
		t.Error("WithSlowSubscriberCallback: not chainable")
	}
	v.slowSubscriber()
	if !called {
		t.Error("slowSubscriber not installed")
	}
}

// --- LogRing / ringFor / registerRing / unregisterRing ---------

func TestJailerVMM_RingLifecycle_Mega4(t *testing.T) {
	t.Parallel()
	v := NewJailerVMM("/srv/fc", 0)

	// ringFor on empty map → nil.
	if got := v.LogRing("missing"); got != nil {
		t.Errorf("LogRing(missing) = %v, want nil", got)
	}

	// registerRing → returns a ring; LogRing returns it.
	r := v.registerRing("i-1")
	if r == nil {
		t.Fatal("registerRing: nil")
	}
	if got := v.LogRing("i-1"); got != r {
		t.Error("LogRing(i-1) != registerRing result")
	}

	// Re-registering replaces (old is closed).
	r2 := v.registerRing("i-1")
	if r2 == r {
		t.Error("re-register should replace, not return same")
	}

	// ringWriter.Write delegates.
	w := &ringWriter{ring: r2, stream: "stdout"}
	n, err := w.Write([]byte("hi\n"))
	if err != nil {
		t.Fatalf("ringWriter.Write: %v", err)
	}
	if n != 3 {
		t.Errorf("n = %d, want 3", n)
	}

	// unregisterRing closes + removes; idempotent.
	v.unregisterRing("i-1")
	v.unregisterRing("i-1") // idempotent — no panic
	if got := v.LogRing("i-1"); got != nil {
		t.Errorf("after unregister: %v, want nil", got)
	}
}

// --- exportMax / destroyWaitFor --------------------------------

func TestJailerVMM_ExportAndDestroyWait_Mega4(t *testing.T) {
	t.Parallel()
	// Default values from constructor.
	v := NewJailerVMM("/srv/fc", 0)
	if v.exportMaxBytes != 0 {
		t.Errorf("exportMaxBytes = %d, want 0 (lazy-resolved at first export)", v.exportMaxBytes)
	}
	if v.destroyWait != 10*time.Minute {
		t.Errorf("destroyWait = %v, want 10m", v.destroyWait)
	}
}

// --- helpers ---------------------------------------------------

func containsKV(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if strings.Contains(a, want) {
			return true
		}
	}
	return false
}

func itoa_Mega4(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
