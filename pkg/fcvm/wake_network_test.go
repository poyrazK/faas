package fcvm

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// joiningFakeVMM is fakeVMM plus the JailerVMM contract the overlap relies
// on: the VMM joins the wake network before the step that enters it.
type joiningFakeVMM struct {
	*fakeVMM
	restoreSawNetwork atomic.Bool
	bootSawNetwork    atomic.Bool
}

func (*joiningFakeVMM) joinsWakeNetwork() {}

func (v *joiningFakeVMM) Restore(ctx context.Context, l Lease, spec RestoreSpec) error {
	if _, _, err := awaitWakeNetwork(ctx); err != nil {
		return err
	}
	v.restoreSawNetwork.Store(true)
	return v.fakeVMM.Restore(ctx, l, spec)
}

func (v *joiningFakeVMM) Boot(ctx context.Context, l Lease, cfg VMConfig, healthcheckPath string) error {
	if _, _, err := awaitWakeNetwork(ctx); err != nil {
		return err
	}
	v.bootSawNetwork.Store(true)
	return v.fakeVMM.Boot(ctx, l, cfg, healthcheckPath)
}

func restoreWakeRequest(id string) WakeRequest {
	return WakeRequest{
		Instance: id, BaseKey: "/b.ext4", LayerKey: "/l.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby,
		Snapshot: &Snapshot{FCVersion: testFCVersion, StorageKey: "snap/" + id + "/mem", VMStatePath: "/snap/" + id + "/state"},
	}
}

func TestWakeOverlappedNetworkIsReadyBeforeRestoreProceeds(t *testing.T) {
	run := &fakeRunner{}
	vmm := &joiningFakeVMM{fakeVMM: &fakeVMM{}}
	m := newTestManager(run, vmm)

	inst, err := m.Wake(context.Background(), restoreWakeRequest("i1"))
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Fatalf("method = %s, want restore", inst.Method)
	}
	if !vmm.restoreSawNetwork.Load() {
		t.Fatal("restore did not join the overlapped wake network")
	}
	if !run.ran("netns add fc-i1") {
		t.Fatal("wake network was never built")
	}
}

// A failed namespace build must surface as a network failure and must not
// trigger the restore→cold-boot fallback, which needs the same namespace.
func TestWakeOverlappedNetworkFailureSkipsColdBootFallbackAndLeaksNothing(t *testing.T) {
	run := &fakeRunner{failOn: "tuntap add tap0"}
	vmm := &joiningFakeVMM{fakeVMM: &fakeVMM{}}
	m := newTestManager(run, vmm)

	_, err := m.Wake(context.Background(), restoreWakeRequest("i1"))
	if err == nil {
		t.Fatal("Wake succeeded with a failing network build")
	}
	if !strings.Contains(err.Error(), "network setup") {
		t.Fatalf("err = %v, want a network setup failure", err)
	}
	if vmm.bootSawNetwork.Load() || vmm.bootCount != 0 {
		t.Fatal("a failed wake network fell back to a cold boot")
	}
	if m.LeasedCount() != 0 {
		t.Fatalf("leased = %d, want 0 after a failed overlapped network", m.LeasedCount())
	}
	if !run.ran("netns del fc-i1") {
		t.Fatal("teardown did not delete the half-built namespace")
	}
}

// A VMM that does not join keeps the serial order its tests rely on.
func TestWakeNonJoiningVMMBuildsNetworkFirst(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)
	if _, err := m.Wake(context.Background(), restoreWakeRequest("i1")); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if !run.ran("netns add fc-i1") {
		t.Fatal("serial wake did not build the network")
	}
}
