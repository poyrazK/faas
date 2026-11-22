// adr: 20261122132055 §2, §5 — schedd admits a persistent session within the
// persistent shapes, resumes a paused one from its own capture (never a warm
// snapshot's bookkeeping), and relays a pause to vmmd: the stream's end after
// paused is clean.
package sched_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

const sessionCapture = "snap/sandbox-session-acct/captures/c1/v2/mem"

func TestSandboxRunnerResumesFromTheSessionCapture(t *testing.T) {
	runner, router, ledger, node := sandboxRunnerFixture(t, "base/sandbox-layer-v1-amd64.ext4")
	a := sandboxAdmission()
	a.Persistent, a.RAMMB, a.VCPU, a.CPUMillicores = true, 8192, 4, 4000
	a.Resume = &sched.SandboxResume{NodeID: node, Snapshot: sched.AgentRunSnapshot{StorageKey: sessionCapture,
		VMStateStorageKey: "snap/sandbox-session-acct/captures/c1/v2/vmstate", FCVersion: "1.10.0", MemBytes: 1 << 30}}
	control := make(chan sched.SandboxControl, 1)
	done := make(chan error, 1)
	go func() { done <- runner.RunSandbox(context.Background(), a, nil, sched.SandboxHooks{}, control) }()
	control <- sched.SandboxClose
	if err := <-done; err != nil {
		t.Fatalf("resume: %v", err)
	}
	s := router.spec
	if !s.SessionCapture || s.Snapshot == nil || *s.Snapshot != a.Resume.Snapshot || s.MemSizeMiB != 8192 || s.VCPU != 4 || router.nodeID != node {
		t.Fatalf("spec = %+v on %s", s, router.nodeID)
	}
	if ledger.ResidentFor(sched.SandboxInstanceID(a.SandboxID)) {
		t.Fatal("the reservation outlived the resumed session")
	}
}

func TestSandboxRunnerPersistentShapesAndResumeRefusals(t *testing.T) {
	runner, _, _, _ := sandboxRunnerFixture(t, "base/sandbox-layer-v1-amd64.ext4")
	resume := &sched.SandboxResume{Snapshot: sched.AgentRunSnapshot{StorageKey: sessionCapture}}
	idx := api.PlanPro.PlanIndex()
	for name, edit := range map[string]func(*sched.SandboxAdmission){
		"an ephemeral session at the persistent RAM": func(a *sched.SandboxAdmission) { a.RAMMB = api.SandboxPersistentRAMMBMax[idx] },
		"a persistent session above its RAM": func(a *sched.SandboxAdmission) {
			a.Persistent, a.RAMMB = true, api.SandboxPersistentRAMMBMax[idx]+512
		},
		"a persistent session above its vCPUs": func(a *sched.SandboxAdmission) {
			a.Persistent, a.VCPU = true, api.SandboxPersistentVCPUMax[idx]+1
		},
		"a persistent session on hobby above its RAM": func(a *sched.SandboxAdmission) {
			a.Persistent, a.Plan, a.RAMMB = true, api.PlanHobby, api.SandboxPersistentRAMMBMax[api.PlanHobby.PlanIndex()]+512
		},
		"a resume of an ephemeral session":  func(a *sched.SandboxAdmission) { a.Resume = resume },
		"a resume with a workspace archive": func(a *sched.SandboxAdmission) { a.Persistent, a.Resume, a.WorkspaceKey = true, resume, "w" },
		"a resume without its capture": func(a *sched.SandboxAdmission) {
			a.Persistent, a.Resume = true, &sched.SandboxResume{NodeID: "n"}
		},
	} {
		a := sandboxAdmission()
		edit(&a)
		var invalid *sched.AgentRunInvalidError
		if err := runner.RunSandbox(context.Background(), a, nil, sched.SandboxHooks{}, nil); !errors.As(err, &invalid) {
			t.Errorf("%s: %v, want AgentRunInvalidError", name, err)
		}
	}
}

// pauseVmmd boots a restored session and answers its pause with paused, then
// ends the stream as vmmd does.
type pauseVmmd struct {
	vmmdpb.UnimplementedVmmdServer
	start *vmmdpb.RunSandboxStart
	pause *vmmdpb.RunSandboxPause
}

func (v *pauseVmmd) RunSandbox(stream vmmdpb.Vmmd_RunSandboxServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	v.start = first.GetStart()
	_ = stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Booted{Booted: &vmmdpb.RunSandboxBooted{
		Method: vmmdpb.WakeMethod_WAKE_RESTORE, BootMs: 5}}})
	msg, err := stream.Recv()
	if err != nil || msg.GetPause() == nil {
		return errors.New("expected a pause")
	}
	v.pause = msg.GetPause()
	_ = stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_PauseRefused{PauseRefused: &vmmdpb.RunSandboxPauseRefused{Reason: "busy"}}})
	return stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Paused{Paused: &vmmdpb.RunSandboxPaused{
		Snapshot: &vmmdpb.AgentRunSnapshot{StorageKey: sessionCapture, VmstateStorageKey: "v", FcVersion: "1.10.0", MemBytes: 9},
		VmstateBytes: 3, PauseMs: 4}}})
}

func TestVMMClientRelaysAPauseAndEndsCleanly(t *testing.T) {
	fake := &pauseVmmd{}
	srv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(srv, fake)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	control := make(chan sched.SandboxControl, 1)
	var paused sched.SandboxPaused
	var refused string
	hooks := sched.SandboxHooks{
		Booted: func(context.Context, sched.SandboxBooted) error {
			control <- sched.SandboxControl{Op: sched.SandboxOpPause, CaptureID: "c1"}
			return nil
		},
		PauseRefused: func(_ context.Context, reason string) error { refused = reason; return nil },
		Paused:       func(_ context.Context, p sched.SandboxPaused) error { paused = p; return nil },
	}
	snap := sched.AgentRunSnapshot{StorageKey: sessionCapture, VMStateStorageKey: "v", FCVersion: "1.10.0", MemBytes: 9}
	spec := sched.SandboxSpec{Instance: "sandbox-1", SandboxID: "1", Snapshot: &snap, SessionCapture: true}
	if err := sched.NewVMMClient(conn).RunSandbox(ctx, spec, hooks, control); err != nil {
		t.Fatalf("a paused stream's end: %v", err)
	}
	if !fake.start.GetSessionCapture() || fake.start.GetSnapshot().GetStorageKey() != sessionCapture || fake.pause.GetCaptureId() != "c1" {
		t.Fatalf("start %v, pause %v", fake.start, fake.pause)
	}
	if refused != "busy" || paused.Snapshot != snap || paused.VMStateBytes != 3 || paused.PauseMs != 4 {
		t.Fatalf("refused %q, paused %+v", refused, paused)
	}
}
