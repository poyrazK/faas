//go:build linux

package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func adoptTestProcess(t *testing.T) (*exec.Cmd, resourceProcessIdentity) {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	id, err := readResourceProcessIdentity("/proc", cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	return cmd, id
}

func TestAdoptGuestProcessSignalsAndWatchesThroughPidfds(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	var exits atomic.Int32
	var code atomic.Int32
	v.processExitSink = func(_ string, exitCode int) { exits.Add(1); code.Store(int32(exitCode)) }
	l := leaseForSlot(idLive, 3)
	l.Plan, l.MemoryMaxMiB = api.PlanPro, 512
	// The guest's cgroup scope exists while it runs; adoption rewrites its
	// fence there (TestMain points cgroupRoot at a temporary tree).
	if err := os.MkdirAll(filepath.Join(cgroupRoot, vmCgroupParent(l), PerInstanceScope(l.Instance)), 0o755); err != nil {
		t.Fatal(err)
	}
	_, id := adoptTestProcess(t)
	jail := resourceAsset{Kind: "jail", Path: "/srv/fc/jail/firecracker/" + idLive + "/root"}
	if err := v.adoptGuestProcess(l, id, adoptedOwner{AppID: "app-1", DeploymentID: "dep-1"}, []resourceAsset{jail}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if pid, ok := v.InstancePID(idLive); !ok || pid != id.PID {
		t.Fatalf("InstancePID = %d, %v", pid, ok)
	}
	if v.ringFor(idLive) == nil || len(v.ownedJails[idLive]) != 1 {
		t.Fatal("ring or jail ownership not rebuilt")
	}
	v.mu.Lock()
	cmd, rec := v.proc[idLive], v.recs[idLive]
	v.mu.Unlock()
	if err := killAndConfirmExit(cmd, recordDone(rec), 5*time.Second); err != nil {
		t.Fatalf("kill an adopted process: %v", err)
	}
	if exits.Load() != 1 || code.Load() != -1 {
		t.Fatalf("exit sink calls %d code %d, want one call with -1", exits.Load(), code.Load())
	}
	if _, ok := v.InstancePID(idLive); ok {
		t.Fatal("exited adopted process still reported live")
	}
	// A second adoption of the same instance is refused while it is live.
	_, other := adoptTestProcess(t)
	if err := v.adoptGuestProcess(l, other, adoptedOwner{}, nil, nil, nil); err != nil {
		t.Fatalf("adopt after exit: %v", err)
	}
	if err := v.adoptGuestProcess(l, other, adoptedOwner{}, nil, nil, nil); err == nil {
		t.Fatal("adopted an instance twice")
	}
}

func TestAdoptGuestProcessRefusesAnotherIncarnation(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	l := leaseForSlot(idLive, 3)
	l.Plan, l.MemoryMaxMiB = api.PlanPro, 512
	// The guest's cgroup scope exists while it runs; adoption rewrites its
	// fence there (TestMain points cgroupRoot at a temporary tree).
	if err := os.MkdirAll(filepath.Join(cgroupRoot, vmCgroupParent(l), PerInstanceScope(l.Instance)), 0o755); err != nil {
		t.Fatal(err)
	}
	_, id := adoptTestProcess(t)
	reused := id
	reused.StartTicks++
	if err := v.adoptGuestProcess(l, reused, adoptedOwner{}, nil, nil, nil); err == nil {
		t.Fatal("adopted a process whose start ticks differ from the record")
	}
	if _, ok := v.InstancePID(idLive); ok {
		t.Fatal("refused adoption left a process registered")
	}
}

// Where vmmd shares the host's mount namespace (the e2e harness), the TUN
// source bind the previous vmmd made is still mounted: adoption registers it
// and teardown unmounts it, so the jail can be removed. Under systemd's
// per-start namespace it is gone and nothing is registered.
func TestAdoptTunSourceBindFollowsTheMountNamespace(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	target := filepath.Join(v.chrootRoot(idLive), nativeTunTargetName)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.adoptTunSourceBind(idLive); err != nil {
		t.Fatal(err)
	}
	if n := len(v.bindMounts[idLive]); n != 0 {
		t.Fatalf("registered %d binds for an unmounted TUN target", n)
	}
	if os.Geteuid() != 0 {
		t.Skip("mounting a TUN source stand-in needs root")
	}
	source := filepath.Join(t.TempDir(), "tun")
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := bindFileMount(source, target); err != nil {
		t.Fatalf("bind the stand-in: %v (%s)", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("umount", target).Run() })
	if err := v.adoptTunSourceBind(idLive); err != nil {
		t.Fatal(err)
	}
	if err := v.unmountBindMounts(idLive); err != nil {
		t.Fatalf("teardown of the adopted TUN bind: %v", err)
	}
	if present, err := hostMountPresent(target); err != nil || present {
		t.Fatalf("TUN bind still mounted after teardown: %v, %v", present, err)
	}
	if err := resourceMountTreeClear(filepath.Dir(target)); err != nil {
		t.Fatalf("jail still holds a mount: %v", err)
	}
}
