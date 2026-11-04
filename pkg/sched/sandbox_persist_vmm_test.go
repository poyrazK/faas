// adr: 20261027090013 step 7
package sched_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// persistVmmd boots a session from a workspace, asks for a persist, reports
// it done once granted, and ends once agentd's record arrives.
type persistVmmd struct {
	vmmdpb.UnimplementedVmmdServer
	start    *vmmdpb.RunSandboxStart
	grant    *vmmdpb.RunSandboxPersistGrant
	recorded *vmmdpb.RunSandboxPersistRecorded
}

func (v *persistVmmd) RunSandbox(stream vmmdpb.Vmmd_RunSandboxServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	v.start = first.GetStart()
	_ = stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Booted{Booted: &vmmdpb.RunSandboxBooted{BootMs: 5}}})
	_ = stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_PersistRequested{PersistRequested: &vmmdpb.RunSandboxPersistRequested{RequestId: "r1"}}})
	msg, err := stream.Recv()
	if err != nil || msg.GetPersistGrant() == nil {
		return errors.New("expected a grant")
	}
	v.grant = msg.GetPersistGrant()
	_ = stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_PersistDone{PersistDone: &vmmdpb.RunSandboxPersistDone{
		RequestId: "r1", SnapshotId: v.grant.GetSnapshotId(), Bytes: 7, Sha256: "ff"}}})
	if msg, err = stream.Recv(); err != nil || msg.GetPersistRecorded() == nil {
		return errors.New("expected a record")
	}
	v.recorded = msg.GetPersistRecorded()
	if msg, err = stream.Recv(); err != nil || msg.GetClose() == nil {
		return errors.New("expected close")
	}
	return nil
}

func TestVMMClientRelaysAPersist(t *testing.T) {
	fake := &persistVmmd{}
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
	control := make(chan sched.SandboxControl, 4)
	var done sched.SandboxPersistDone
	hooks := sched.SandboxHooks{
		PersistRequested: func(_ context.Context, id string) error {
			control <- sched.SandboxControl{Op: sched.SandboxOpPersistGrant, PersistGrant: &sched.SandboxPersistGrant{
				RequestID: id, SnapshotID: "snap", StorageKey: "k", MaxBytes: 9, ExpiresAtUnixMs: 3}}
			return nil
		},
		PersistDone: func(_ context.Context, d sched.SandboxPersistDone) error {
			done = d
			control <- sched.SandboxControl{Op: sched.SandboxOpPersistRecorded, PersistRecorded: &sched.SandboxPersistRecorded{RequestID: d.RequestID, Recorded: true}}
			control <- sched.SandboxClose
			return nil
		},
	}
	spec := sched.SandboxSpec{Instance: "sandbox-1", SandboxID: "1", WorkspaceKey: "agents/workspaces/a/s.tar.gz"}
	if err := sched.NewVMMClient(conn).RunSandbox(ctx, spec, hooks, control); err != nil {
		t.Fatal(err)
	}
	if fake.start.GetWorkspaceKey() != spec.WorkspaceKey {
		t.Fatalf("start %v", fake.start)
	}
	if g := fake.grant; g.GetRequestId() != "r1" || g.GetSnapshotId() != "snap" || g.GetStorageKey() != "k" || g.GetMaxBytes() != 9 || g.GetExpiresAtUnixMs() != 3 {
		t.Fatalf("grant %v", g)
	}
	if done.RequestID != "r1" || done.Bytes != 7 || done.SHA256 != "ff" || !fake.recorded.GetRecorded() {
		t.Fatalf("done %+v, recorded %v", done, fake.recorded)
	}
}
