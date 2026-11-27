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
		Snapshot:     &vmmdpb.AgentRunSnapshot{StorageKey: sessionCapture, VmstateStorageKey: "v", FcVersion: "1.10.0", MemBytes: 9},
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

// adr: 20261122132055 §2 — a capture only its node holds resumes there or
// not at all: schedd answers no capacity before it records a placement, and
// agentd tries again. A node that is draining or restarting still holds the
// capture, so the resume waits for it; only a node that is gone is given up.
func TestSandboxRunnerResumeRequiresTheCapturesNode(t *testing.T) {
	runner, router, ledger, full, store := sandboxRunnerPoolStoreFixture(t, "base/sandbox-layer-v1-amd64.ext4", "agents-1", "agents-2")
	// The capture's node has no room left.
	if err := ledger.Admit(sched.Request{Instance: "filler", NodeID: full, NodeCeilingMB: 47600, Plan: api.PlanScale, RAMMB: 47000}); err != nil {
		t.Fatal(err)
	}
	a := sandboxAdmission()
	a.Persistent = true
	a.Resume = &sched.SandboxResume{NodeID: full, RequireNode: true,
		Snapshot: sched.AgentRunSnapshot{StorageKey: sessionCapture, FCVersion: "1.10.0", MemBytes: 1}}
	placedOn := ""
	placed := func(p sched.AgentRunPlacement) error { placedOn = p.NodeID; return nil }
	err := runner.RunSandbox(context.Background(), a, placed, sched.SandboxHooks{}, nil)
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeCapacity {
		t.Fatalf("resume away from its capture's node = %v, want a capacity refusal", err)
	}
	if ledger.ResidentFor(sched.SandboxInstanceID(a.SandboxID)) || placedOn != "" {
		t.Fatalf("a refused resume kept its reservation or recorded node %q", placedOn)
	}
	// The capture's node is draining: the resume waits rather than restore
	// nothing elsewhere.
	if err := store.SetComputeNodeActive(context.Background(), full, false); err != nil {
		t.Fatal(err)
	}
	ledger.Release("filler")
	// A placement here would boot and hold the session until ctx ends.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = runner.RunSandbox(ctx, a, placed, sched.SandboxHooks{}, nil)
	if !errors.As(err, &problem) || problem.Code != api.CodeCapacity || placedOn != "" || router.nodeID != "" {
		t.Fatalf("resume while its capture's node is inactive = %v, placed %q, ran on %q", err, placedOn, router.nodeID)
	}
	// A node that left the pool holds nothing: the resume goes anywhere.
	a.Resume.NodeID = "a-node-that-is-gone"
	ctx, cancel = context.WithCancel(context.Background())
	booted := func(context.Context, sched.SandboxBooted) error { cancel(); return nil }
	_ = runner.RunSandbox(ctx, a, nil, sched.SandboxHooks{Booted: booted}, nil)
	if router.nodeID == "" || router.nodeID == full || router.spec.Snapshot == nil {
		t.Fatalf("resume of a gone node's capture placed on %q with %+v", router.nodeID, router.spec.Snapshot)
	}
}

// adr: 20261122132055 §4 — a cold resume prefers the node holding the
// volume's newest copy; once that node left the pool, its copy and the
// backups only it held are gone, so vmmd restores the newest stored backup.
func TestSandboxRunnerColdResumeFollowsTheVolume(t *testing.T) {
	runner, router, _, _, store := sandboxRunnerPoolStoreFixture(t, "base/sandbox-layer-v1-amd64.ext4", "agents-1", "agents-2")
	run := func(a sched.SandboxAdmission) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		booted := func(context.Context, sched.SandboxBooted) error { cancel(); return nil }
		_ = runner.RunSandbox(ctx, a, nil, sched.SandboxHooks{Booted: booted}, nil)
	}
	for _, name := range []string{"agents-1", "agents-2"} {
		node, err := store.ComputeNodeByName(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		a := sandboxAdmission()
		a.Persistent = true
		a.Volume = sched.SandboxVolume{GB: 10, BackupID: "pause", StoredBackupID: "periodic", NodeID: node.ID}
		run(a)
		if router.nodeID != node.ID || router.spec.Volume.BackupID != "pause" {
			t.Fatalf("cold resume of a volume on %s ran on %q with backup %q", name, router.nodeID, router.spec.Volume.BackupID)
		}
	}
	a := sandboxAdmission()
	a.Persistent = true
	a.Volume = sched.SandboxVolume{GB: 10, BackupID: "pause", StoredBackupID: "periodic", NodeID: "a-node-that-is-gone"}
	run(a)
	if router.nodeID == "" || router.spec.Volume.BackupID != "periodic" {
		t.Fatalf("cold resume of a gone node's volume ran on %q with backup %q", router.nodeID, router.spec.Volume.BackupID)
	}
}
