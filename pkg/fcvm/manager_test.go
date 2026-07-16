package fcvm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeRunner records every command and can be told to fail a specific one.
type fakeRunner struct {
	mu       sync.Mutex
	commands [][]string
	failOn   string // substring; the first matching command errors
}

func (f *fakeRunner) Run(_ context.Context, argv []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, argv)
	if f.failOn != "" && strings.Contains(strings.Join(argv, " "), f.failOn) {
		return fmt.Errorf("fake failure on %q", f.failOn)
	}
	return nil
}

func (f *fakeRunner) ran(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.Contains(strings.Join(c, " "), substr) {
			return true
		}
	}
	return false
}

// fakeVMM records calls and can be told to fail Boot/Restore/Snapshot.
type fakeVMM struct {
	mu          sync.Mutex
	bootErr     error
	restoreErr  error
	snapErr     error
	killed      []string
	restored    []string
	snapshotted []string
	bootCount   int
}

func (v *fakeVMM) Boot(_ context.Context, l Lease, _ VMConfig) error {
	v.mu.Lock()
	v.bootCount++
	v.mu.Unlock()
	return v.bootErr
}

func (v *fakeVMM) Restore(_ context.Context, l Lease, _ RestoreSpec) error {
	v.mu.Lock()
	v.restored = append(v.restored, l.Instance)
	v.mu.Unlock()
	return v.restoreErr
}

func (v *fakeVMM) Snapshot(_ context.Context, l Lease, _ SnapshotSpec) (SnapshotInfo, error) {
	v.mu.Lock()
	v.snapshotted = append(v.snapshotted, l.Instance)
	v.mu.Unlock()
	return SnapshotInfo{MemBytes: 4096}, v.snapErr
}

func (v *fakeVMM) Kill(_ context.Context, l Lease) error {
	v.mu.Lock()
	v.killed = append(v.killed, l.Instance)
	v.mu.Unlock()
	return nil
}

func (v *fakeVMM) restoredInstance(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, r := range v.restored {
		if r == id {
			return true
		}
	}
	return false
}

func (v *fakeVMM) boots() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.bootCount
}

const testFCVersion = "1.7.0"

func usableSnapshot() *Snapshot {
	return &Snapshot{DeploymentID: "d1", FCVersion: testFCVersion, MemPath: "/snap/mem", VMStatePath: "/snap/state"}
}

func (v *fakeVMM) killedInstance(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, k := range v.killed {
		if k == id {
			return true
		}
	}
	return false
}

func req(id string) ColdBootRequest {
	return ColdBootRequest{Instance: id, BasePath: "/b.ext4", LayerPath: "/l.ext4", VcpuCount: 2, MemSizeMiB: 128}
}

func newTestManager(run Runner, vmm VMM) *Manager {
	return NewManager(run, vmm, Paths{Kernel: "/srv/fc/base/vmlinux-6.1"}, testFCVersion, nil)
}

func TestColdBootSuccessTracksInstance(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.ColdBoot(context.Background(), req("i1"))
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if inst.Lease.UID < JailUIDBase {
		t.Errorf("lease uid not assigned: %d", inst.Lease.UID)
	}
	if m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatalf("live=%d leased=%d, want 1/1", m.LiveCount(), m.LeasedCount())
	}
	if !run.ran("netns add fc-i1") {
		t.Error("network setup did not run")
	}
}

func TestColdBootNetworkFailureLeaksNothing(t *testing.T) {
	// Fail midway through network setup; the lease must be released and teardown
	// attempted so leakcheck stays clean.
	run := &fakeRunner{failOn: "tuntap add tap0"}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	if _, err := m.ColdBoot(context.Background(), req("i1")); err == nil {
		t.Fatal("expected cold boot to fail")
	}
	if m.LiveCount() != 0 {
		t.Errorf("live=%d, want 0 after failed boot", m.LiveCount())
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leased=%d, want 0 after failed boot — LEASE LEAK", m.LeasedCount())
	}
	if !run.ran("netns del fc-i1") {
		t.Error("teardown did not attempt to delete the netns")
	}
}

func TestColdBootVMFailureLeaksNothing(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{bootErr: fmt.Errorf("kvm exploded")}
	m := newTestManager(run, vmm)

	if _, err := m.ColdBoot(context.Background(), req("i1")); err == nil {
		t.Fatal("expected cold boot to fail")
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leased=%d, want 0 — LEASE LEAK after VM boot failure", m.LeasedCount())
	}
	if !vmm.killedInstance("i1") {
		t.Error("VM was not killed on the cleanup path")
	}
	if !run.ran("netns del fc-i1") {
		t.Error("network was not torn down on VM boot failure")
	}
}

func TestDestroyReleasesResources(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	if _, err := m.ColdBoot(context.Background(), req("i1")); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(context.Background(), "i1"); err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Errorf("live=%d leased=%d, want 0/0 after destroy", m.LiveCount(), m.LeasedCount())
	}
	if !vmm.killedInstance("i1") {
		t.Error("destroy did not kill the VM")
	}
}

func TestDestroyUnknownIsNoop(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.Destroy(context.Background(), "ghost"); err != nil {
		t.Errorf("destroying unknown instance should be a no-op, got %v", err)
	}
}

// TestConcurrentBootAndDestroyNoLeak mirrors the M1 acceptance shape (boot many,
// tear all down, zero leaks) at the orchestration level.
func TestConcurrentBootAndDestroyNoLeak(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	const n = 50 // M1: boot 50 VMs concurrently

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := m.ColdBoot(context.Background(), req(fmt.Sprintf("i%d", i))); err != nil {
				t.Errorf("boot i%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if m.LiveCount() != n || m.LeasedCount() != n {
		t.Fatalf("after boot: live=%d leased=%d, want %d/%d", m.LiveCount(), m.LeasedCount(), n, n)
	}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := m.Destroy(context.Background(), fmt.Sprintf("i%d", i)); err != nil {
				t.Errorf("destroy i%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("after teardown: live=%d leased=%d, want 0/0 (LEAK)", m.LiveCount(), m.LeasedCount())
	}
}
