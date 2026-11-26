// adr: 20261122132055 §2 — vmmd pauses a persistent session's VM on agentd's
// pause: it thaws a frozen VM, captures it under the account's session
// namespace, sends paused and ends the stream; a pause it cannot take leaves
// the VM running and answers pause_refused; a capture that lost the VM ends
// the stream with an error. A session capture restores only into a session
// of its own account and without a workspace archive.

package vmmdgrpc_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

const testCaptureID = "0b6f5c6e-3b0a-4f43-9a55-2f6c1b9d7e10"

// pausingVMM is a session VMM that pauses: fail makes the capture fail,
// keeping the VM when keep is set.
type pausingVMM struct {
	*sandboxRunVMM
	fail, keep   bool
	pauses       []fcvm.SandboxPause
	publishFails int
	published    []string
}

func (v *pausingVMM) PauseSandbox(_ context.Context, p fcvm.SandboxPause) (fcvm.SandboxPaused, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.pauses = append(v.pauses, p)
	if v.frozen {
		return fcvm.SandboxPaused{}, true, fcvm.ErrInstanceFrozen
	}
	if v.fail {
		return fcvm.SandboxPaused{}, v.keep, errors.New("capture failed")
	}
	mem := state.SnapshotCaptureMemKey(storagekey.SandboxSessionSnapshotNamespace(p.AccountID), "", p.CaptureID)
	return fcvm.SandboxPaused{Snapshot: &fcvm.Snapshot{StorageKey: mem, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: mem}),
		FCVersion: "1.10.0", MemBytes: 1 << 30}, Info: fcvm.SnapshotInfo{MemBytes: 1 << 30, VMStateBytes: 40_000}, HasBacking: true}, false, nil
}

// PublishSandboxCapture fails v.publishFails times, then records the
// upload (ADR-20261122132055 §2).
func (v *pausingVMM) PublishSandboxCapture(_ context.Context, memKey string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.publishFails > 0 {
		v.publishFails--
		return errors.New("store unavailable")
	}
	v.published = append(v.published, memKey)
	return nil
}

func (v *pausingVMM) UnpinSandboxCapture(string) error { return nil }

func (v *pausingVMM) PublishPendingSandboxCaptures(context.Context, time.Duration) (int, error) {
	return 0, nil
}

func (v *pausingVMM) uploads() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.published...)
}

func pauseFrame(id string) *vmmdpb.RunSandboxRequest {
	return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Pause{Pause: &vmmdpb.RunSandboxPause{CaptureId: id}}}
}

func TestRunSandboxPausesAFrozenVMAndEndsTheStream(t *testing.T) {
	vmm := &pausingVMM{sandboxRunVMM: newSandboxRunVMM()}
	cli := newSandboxRunClient(t, vmm, true)
	stream := bootSandbox(t, cli)
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Freeze{Freeze: &vmmdpb.RunSandboxFreeze{}}}); err != nil {
		t.Fatal(err)
	}
	recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetFrozen() != nil })
	if err := stream.Send(pauseFrame(testCaptureID)); err != nil {
		t.Fatal(err)
	}
	ev := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetPaused() != nil })
	snap := ev.GetPaused().GetSnapshot()
	if !storagekey.IsSandboxSessionSnapshotKey(snap.GetStorageKey(), "acct-1") || snap.GetFcVersion() != "1.10.0" ||
		snap.GetMemBytes() != 1<<30 || ev.GetPaused().GetVmstateBytes() != 40_000 || !ev.GetPaused().GetHasBacking() {
		t.Fatalf("paused = %v", ev.GetPaused())
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("after paused: %v, want the stream's end", err)
	}
	if _, _, frozen, thaws := vmm.state(); frozen || thaws != 1 {
		t.Fatalf("the pause did not thaw the VM first: frozen %v after %d thaws", frozen, thaws)
	}
	if p := vmm.pauses; len(p) != 1 || p[0].Instance != "sbx-vm" || p[0].SandboxID != "sbx-1" || p[0].AccountID != "acct-1" || p[0].CaptureID != testCaptureID {
		t.Fatalf("pauses = %+v", p)
	}
	// The capture reaches the shared store in the background, after the
	// stream ended.
	waitUntil(t, func() bool { return len(vmm.uploads()) == 1 })
	if vmm.uploads()[0] != snap.GetStorageKey() {
		t.Fatalf("uploaded %v", vmm.uploads())
	}
}

func TestRunSandboxRetriesAFailedCaptureUpload(t *testing.T) {
	vmm := &pausingVMM{sandboxRunVMM: newSandboxRunVMM(), publishFails: 1}
	cli := newSandboxRunClient(t, vmm, true)
	stream := bootSandbox(t, cli)
	if err := stream.Send(pauseFrame(testCaptureID)); err != nil {
		t.Fatal(err)
	}
	recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetPaused() != nil })
	waitUntil(t, func() bool { return len(vmm.uploads()) == 1 })
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunSandboxPauseRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vmm     vmmdgrpc.VmmdAPI
		capture string
		reason  string
	}{
		{"no pause in the vmm", newSandboxRunVMM(), testCaptureID, "unsupported"},
		{"a capture ID that is not a UUID", &pausingVMM{sandboxRunVMM: newSandboxRunVMM()}, "../x", "invalid"},
		{"a capture that kept the VM", &pausingVMM{sandboxRunVMM: newSandboxRunVMM(), fail: true, keep: true}, testCaptureID, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli := newSandboxRunClient(t, tc.vmm, true)
			stream := bootSandbox(t, cli)
			if err := stream.Send(pauseFrame(tc.capture)); err != nil {
				t.Fatal(err)
			}
			ev := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetPauseRefused() != nil || ev.GetPaused() != nil })
			if ev.GetPauseRefused().GetReason() != tc.reason {
				t.Fatalf("event = %v, want refused %q", ev, tc.reason)
			}
			// The VM runs on: a call still reaches it, and close ends it.
			runSandboxCall(t, cli)
			if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunSandboxPauseThatLostTheVMEndsTheStream(t *testing.T) {
	vmm := &pausingVMM{sandboxRunVMM: newSandboxRunVMM(), fail: true}
	cli := newSandboxRunClient(t, vmm, true)
	stream := bootSandbox(t, cli)
	if err := stream.Send(pauseFrame(testCaptureID)); err != nil {
		t.Fatal(err)
	}
	for {
		ev, err := stream.Recv()
		if err != nil {
			if status.Code(err) != codes.Aborted {
				t.Fatalf("a lost VM ended the stream with %v, want Aborted", err)
			}
			return
		}
		if ev.GetPaused() != nil || ev.GetPauseRefused() != nil {
			t.Fatalf("a lost VM answered %v", ev)
		}
	}
}

func TestRunSandboxRestoresOnlyItsAccountsSessionCapture(t *testing.T) {
	own := state.SnapshotCaptureMemKey(storagekey.SandboxSessionSnapshotNamespace("acct-1"), "", testCaptureID)
	other := state.SnapshotCaptureMemKey(storagekey.SandboxSessionSnapshotNamespace("acct-2"), "", testCaptureID)
	warm := state.SnapshotCaptureMemKey(storagekey.SandboxSnapshotNamespace("acct-1", false), "", testCaptureID)
	snap := func(key string) *vmmdpb.AgentRunSnapshot {
		return &vmmdpb.AgentRunSnapshot{StorageKey: key, VmstateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}),
			FcVersion: "1.10.0", MemBytes: 1 << 30}
	}
	for _, tc := range []struct {
		name string
		edit func(*vmmdpb.RunSandboxStart)
		ok   bool
	}{
		{"its own capture", func(s *vmmdpb.RunSandboxStart) { s.Snapshot = snap(own) }, true},
		{"another account's", func(s *vmmdpb.RunSandboxStart) { s.Snapshot = snap(other) }, false},
		{"a warm snapshot", func(s *vmmdpb.RunSandboxStart) { s.Snapshot = snap(warm) }, false},
		{"no snapshot", func(*vmmdpb.RunSandboxStart) {}, false},
		{"with a workspace archive", func(s *vmmdpb.RunSandboxStart) {
			s.Snapshot, s.WorkspaceKey = snap(own), "sandbox-workspaces/acct-1/x.tar.zst"
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := newSandboxRunVMM()
			cli := newSandboxRunClient(t, vmm, true)
			stream, err := cli.RunSandbox(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			start := sandboxRunStart()
			start.GetStart().SessionCapture = true
			tc.edit(start.GetStart())
			if err := stream.Send(start); err != nil {
				t.Fatal(err)
			}
			ev, err := stream.Recv()
			if !tc.ok {
				if status.Code(err) != codes.InvalidArgument {
					t.Fatalf("start = %v, %v; want InvalidArgument", ev, err)
				}
				return
			}
			if err != nil || ev.GetBooted() == nil {
				t.Fatalf("start = %v, %v", ev, err)
			}
			vmm.mu.Lock()
			wake := vmm.woken[0]
			vmm.mu.Unlock()
			if !wake.SessionCapture || wake.Snapshot == nil || wake.Snapshot.StorageKey != own {
				t.Fatalf("wake = %+v", wake)
			}
		})
	}
}
