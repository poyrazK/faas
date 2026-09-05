// vmm_pure_extra_test.go — fill pkg/fcvm/vmm.go coverage of the pure
// path-helper / projection / ring-register surface. Every function
// touched here is currently at 0% in the pre-PR report; none of them
// require KVM or root, only a small in-process fixture.
//
// Whitebox `package fcvm` (matches every existing pkg/fcvm test).

package fcvm

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

// --- Pure path helpers (chrootRoot / socketPath / vsockUDSSock) ----

func TestJailerVMM_ChrootPathHelpers(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	v.fcName = "firecracker-v1.7.0" // simulate the resolved chroot basename

	const instance = "inst-1"
	wantRoot := filepath.Join("/srv/fc/jail", "firecracker-v1.7.0", instance, "root")
	if got := v.chrootRoot(instance); got != wantRoot {
		t.Errorf("chrootRoot = %q, want %q", got, wantRoot)
	}
	if got := v.socketPath(instance); got != filepath.Join(wantRoot, APISockName) {
		t.Errorf("socketPath = %q, want %q", got, filepath.Join(wantRoot, APISockName))
	}
	if got := v.vsockUDSSock(instance); got != filepath.Join(wantRoot, VsockUDSSocketName) {
		t.Errorf("vsockUDSSock = %q, want %q", got, filepath.Join(wantRoot, VsockUDSSocketName))
	}
}

// --- Ring register/unregister/log round-trip -----------------------

func TestJailerVMM_RingRegister_LogRing_RoundTrip(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)

	if r := v.LogRing("missing"); r != nil {
		t.Errorf("LogRing missing instance: %v, want nil", r)
	}
	if r := v.ringFor("missing"); r != nil {
		t.Errorf("ringFor missing instance: %v, want nil", r)
	}

	r := v.registerRing("inst-1")
	if r == nil {
		t.Fatal("registerRing returned nil")
	}
	if got := v.LogRing("inst-1"); got != r {
		t.Errorf("LogRing after register: %p != registered %p", got, r)
	}

	// Re-registering the same instance must close the old ring and
	// return a fresh pointer (Boot/Restore use this idempotency).
	r2 := v.registerRing("inst-1")
	if r2 == nil {
		t.Fatal("re-registerRing returned nil")
	}
	if r2 == r {
		t.Error("re-registerRing returned same ring; want a fresh one")
	}
}

func TestJailerVMM_RegisterRing_WiresSlowSubscriberCallback(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	var calls int
	v.WithSlowSubscriberCallback(func() { calls++ })

	ring := v.registerRing("inst-1")
	if ring == nil {
		t.Fatal("registerRing returned nil")
	}
	// Callback is installed; we can't force a "slow subscriber"
	// emit from outside the ring, but verify the install path
	// doesn't panic and the chain is fluent.
	if got := v.WithSlowSubscriberCallback(nil); got != v {
		t.Error("WithSlowSubscriberCallback(nil): not chainable")
	}
	_ = calls
}

func TestJailerVMM_RegisterRing_WiresLogEvictionCallback(t *testing.T) {
	v := NewJailerVMM("/srv/faas/jail", 30*time.Second)
	v.WithLogEvictionCallback(func(instance string, line logbuf.Line) {
		_ = instance
		_ = line
	})
	if v.evictedLine == nil {
		t.Fatal("evictedLine callback not installed")
	}
	if got := v.WithLogEvictionCallback(nil); got != v {
		t.Error("WithLogEvictionCallback(nil): not chainable")
	}
}

func TestJailerVMM_UnregisterRing_Idempotent(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	// Unknown instance → silent no-op (no panic).
	v.unregisterRing("never-registered")
	v.unregisterRing("never-registered")

	// Registered → gone after unregister.
	v.registerRing("inst-1")
	v.unregisterRing("inst-1")
	if got := v.LogRing("inst-1"); got != nil {
		t.Errorf("LogRing after unregister: %v, want nil", got)
	}
}

// --- NewJailerVMM constructor -------------------------------------

func TestNewJailerVMM_DefaultReadyTimeout(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 0)
	if v.readyTimeout != 30*time.Second {
		t.Errorf("readyTimeout = %v, want 30s", v.readyTimeout)
	}
}

func TestNewJailerVMM_ExplicitReadyTimeout(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 5*time.Second)
	if v.readyTimeout != 5*time.Second {
		t.Errorf("readyTimeout = %v, want 5s", v.readyTimeout)
	}
}

// --- Pure projections (projectedWorkloadBytes) --------------------

func TestProjectedWorkloadManifestBytes_NilShape(t *testing.T) {
	// All fields zero → only the fixedOverhead contributes.
	got := projectedWorkloadManifestBytes(WorkloadSpec{})
	want := int64(128)
	if got != want {
		t.Errorf("empty spec: got %d, want %d (fixedOverhead)", got, want)
	}
}

func TestProjectedWorkloadManifestBytes_NameMultiplier(t *testing.T) {
	// Names are doubled (escape-budget per the spec).
	got := projectedWorkloadManifestBytes(WorkloadSpec{Name: "abc"})
	want := int64(len("abc"))*2 + 128
	if got != want {
		t.Errorf("name only: got %d, want %d", got, want)
	}
}

func TestProjectedWorkloadManifestBytes_CmdAndEntrypoint(t *testing.T) {
	got := projectedWorkloadManifestBytes(WorkloadSpec{
		Name:       "x",
		Cmd:        []string{"a", "bc"},
		Entrypoint: []string{"/bin/sh"},
	})
	// name: 1*2=2, cmd: (1+2)*2=6 (each cmd entry is len*2), entrypoint: 7*2=14, fixed: 128.
	want := int64(2 + 6 + 14 + 128)
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestProjectedWorkloadRosterBytes_Empty(t *testing.T) {
	got := projectedWorkloadRosterBytes(WorkloadSpec{}, nil)
	want := projectedWorkloadManifestBytes(WorkloadSpec{}) + 64
	if got != want {
		t.Errorf("empty roster: got %d, want %d", got, want)
	}
}

func TestProjectedWorkloadRosterBytes_WithSidecars(t *testing.T) {
	main := WorkloadSpec{Name: "main"}
	sc := []WorkloadSpec{{Name: "redis"}, {Name: "sidecar-2"}}
	got := projectedWorkloadRosterBytes(main, sc)
	want := projectedWorkloadManifestBytes(main) + projectedWorkloadManifestBytes(sc[0]) + projectedWorkloadManifestBytes(sc[1]) + 64
	if got != want {
		t.Errorf("with sidecars: got %d, want %d", got, want)
	}
}

// --- Pure helpers (stableReadOnlyName / sidecarDriveImageName) ----

func TestStableReadOnlyName_FaasSnapReturnsFallback(t *testing.T) {
	if got := stableReadOnlyName("/some/where/faas-snap-abc123", "fallback.ext4"); got != "fallback.ext4" {
		t.Errorf("faas-snap- prefix: got %q, want fallback.ext4", got)
	}
}

func TestStableReadOnlyName_RegularReturnsBasename(t *testing.T) {
	if got := stableReadOnlyName("/some/where/base.ext4", "fallback.ext4"); got != "base.ext4" {
		t.Errorf("regular file: got %q, want base.ext4", got)
	}
}

func TestSidecarDriveImageName(t *testing.T) {
	cases := []struct {
		idx  int
		want string
	}{
		{0, "sidecar-0.ext4"},
		{1, "sidecar-1.ext4"},
		{5, "sidecar-5.ext4"},
	}
	for _, c := range cases {
		if got := sidecarDriveImageName(c.idx); got != c.want {
			t.Errorf("idx=%d: got %q, want %q", c.idx, got, c.want)
		}
	}
}

// --- exportMax / destroyWaitFor -----------------------------------

func TestJailerVMM_ExportMax_ZeroUsesApiDefault(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	if v.exportMax() <= 0 {
		t.Errorf("exportMax(zero) = %d, want api default > 0", v.exportMax())
	}
}

func TestJailerVMM_ExportMax_ExplicitOverride(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	v.exportMaxBytes = 1024
	if got := v.exportMax(); got != 1024 {
		t.Errorf("exportMax(override) = %d, want 1024", got)
	}
}

func TestJailerVMM_DestroyWaitFor_NoExportDir(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	v.destroyWait = 5 * time.Second
	if got := v.destroyWaitFor("", 0); got != 5*time.Second {
		t.Errorf("no export: got %v, want 5s", got)
	}
}

func TestJailerVMM_DestroyWaitFor_WithExportAppliesBuilderMinimum(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	v.destroyWait = 1 * time.Second
	got := v.destroyWaitFor("/tmp/out", 0)
	// buildTimeoutSec=0 → api.BuildTimeoutSeconds; minimum = +600s.
	if got <= 1*time.Second {
		t.Errorf("export dir should raise wait: got %v, want > 1s", got)
	}
}

func TestJailerVMM_DestroyWaitFor_ExplicitTimeoutBelowMinimumRaises(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	v.destroyWait = 1 * time.Second
	got := v.destroyWaitFor("/tmp/out", 60) // 60+600=660s minimum
	want := 660 * time.Second
	if got != want {
		t.Errorf("export with explicit timeout: got %v, want %v", got, want)
	}
}

func TestJailerVMM_DestroyWaitFor_HigherWaitPreserved(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	v.destroyWait = 30 * time.Minute
	got := v.destroyWaitFor("", 0)
	if got != 30*time.Minute {
		t.Errorf("higher wait: got %v, want 30m", got)
	}
}
