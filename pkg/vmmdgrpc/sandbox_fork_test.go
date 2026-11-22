package vmmdgrpc_test

// adr: 20261122132055 §3 — vmmd forks a running session on agentd's fork: it
// thaws a frozen VM, captures it under the account's session namespace,
// answers forked and keeps the VM and the stream; the capture uploads in the
// background. A fork it cannot take answers fork_refused, the VM running on.

import (
	"context"
	"errors"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// forkingVMM is a pausing VMM that also forks; failFork fails the capture.
type forkingVMM struct {
	*pausingVMM
	failFork bool
	forks    []fcvm.SandboxFork
}

func (v *forkingVMM) ForkSandbox(_ context.Context, f fcvm.SandboxFork) (fcvm.SandboxForked, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.forks = append(v.forks, f)
	if v.frozen {
		return fcvm.SandboxForked{}, fcvm.ErrInstanceFrozen
	}
	if v.failFork {
		return fcvm.SandboxForked{}, errors.New("capture failed")
	}
	mem := state.SnapshotCaptureMemKey(storagekey.SandboxSessionSnapshotNamespace(f.AccountID), "", f.CaptureID)
	return fcvm.SandboxForked{SandboxPaused: fcvm.SandboxPaused{Snapshot: &fcvm.Snapshot{StorageKey: mem,
		VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: mem}), FCVersion: "1.10.0", MemBytes: 1 << 30},
		Info: fcvm.SnapshotInfo{MemBytes: 1 << 30, VMStateBytes: 40_000}, HasBacking: true}}, nil
}

func forkFrame(id string) *vmmdpb.RunSandboxRequest {
	return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Fork{Fork: &vmmdpb.RunSandboxFork{CaptureId: id}}}
}

func TestRunSandboxForksAndKeepsTheVM(t *testing.T) {
	vmm := &forkingVMM{pausingVMM: &pausingVMM{sandboxRunVMM: newSandboxRunVMM()}}
	cli := newSandboxRunClient(t, vmm, true)
	stream := bootSandbox(t, cli)
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Freeze{Freeze: &vmmdpb.RunSandboxFreeze{}}}); err != nil {
		t.Fatal(err)
	}
	recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetFrozen() != nil })
	if err := stream.Send(forkFrame(testCaptureID)); err != nil {
		t.Fatal(err)
	}
	ev := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetForked() != nil })
	f := ev.GetForked()
	if !storagekey.IsSandboxSessionSnapshotKey(f.GetSnapshot().GetStorageKey(), "acct-1") || f.GetSnapshot().GetMemBytes() != 1<<30 ||
		!f.GetHasBacking() || f.GetVolumeBackupId() != "" {
		t.Fatalf("forked = %v", f)
	}
	if _, destroyed, frozen, thaws := vmm.state(); len(destroyed) != 0 || frozen || thaws != 1 {
		t.Fatalf("after the fork: destroyed %v, frozen %v, %d thaws", destroyed, frozen, thaws)
	}
	waitUntil(t, func() bool { return len(vmm.uploads()) == 1 })
	// The VM runs on: another fork, then a close, on the same stream.
	vmm.mu.Lock()
	vmm.failFork = true
	vmm.mu.Unlock()
	if err := stream.Send(forkFrame("1d2e3f40-5a6b-4c7d-8e9f-0a1b2c3d4e5f")); err != nil {
		t.Fatal(err)
	}
	ev = recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetForkRefused() != nil })
	if ev.GetForkRefused().GetReason() != "failed" {
		t.Fatalf("refused = %v", ev.GetForkRefused())
	}
	if err := stream.Send(forkFrame("not-a-uuid")); err != nil {
		t.Fatal(err)
	}
	ev = recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetForkRefused() != nil })
	if ev.GetForkRefused().GetReason() != "invalid" {
		t.Fatalf("refused = %v", ev.GetForkRefused())
	}
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}); err != nil {
		t.Fatal(err)
	}
	if forks := vmm.forks; len(forks) != 2 || forks[0].SandboxID != "sbx-1" || forks[0].CaptureID != testCaptureID || forks[0].Volumes != nil {
		t.Fatalf("forks = %+v", forks)
	}
}

func TestRunSandboxForkUnsupported(t *testing.T) {
	cli := newSandboxRunClient(t, &pausingVMM{sandboxRunVMM: newSandboxRunVMM()}, true)
	stream := bootSandbox(t, cli)
	if err := stream.Send(forkFrame(testCaptureID)); err != nil {
		t.Fatal(err)
	}
	ev := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetForkRefused() != nil })
	if ev.GetForkRefused().GetReason() != "unsupported" {
		t.Fatalf("refused = %v", ev.GetForkRefused())
	}
}
