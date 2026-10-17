// adr: 570
package sched_test

import (
	"context"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type lifetimeRPC struct {
	vmmdpb.UnimplementedVmmdServer
	receipt chan wire.CorrelationFields
}

func (s *lifetimeRPC) AdoptMigratedInstance(ctx context.Context, _ *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	fields, _ := wire.CorrelationFromIncoming(ctx)
	s.receipt <- fields
	return &vmmdpb.AdoptMigratedInstanceResponse{}, nil
}
func (s *lifetimeRPC) CreateFromSnapshot(ctx context.Context, _ *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	fields, _ := wire.CorrelationFromIncoming(ctx)
	s.receipt <- fields
	return &vmmdpb.WakeResponse{}, nil
}

func TestTargetLifetimeMigrationAndPoolPreserveWakeOnWire(t *testing.T) {
	srv := grpc.NewServer()
	rpc := &lifetimeRPC{receipt: make(chan wire.CorrelationFields, 1)}
	vmmdpb.RegisterVmmdServer(srv, rpc)
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { srv.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough://lifetime", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := sched.NewVMMClient(conn)
	app := sched.AppSpec{AppID: "app", DeploymentID: "deployment", WakeID: "retained-wake"}
	ctx := wire.WithContext(t.Context(), wire.CorrelationFields{WakeID: "unrelated-caller", AppID: "other"})
	for _, pool := range []bool{false, true} {
		if pool {
			_, err = client.CreatePausedFromSnapshot(ctx, "instance", app, sched.SnapshotRef{})
		} else {
			_, err = client.AdoptMigratedInstance(ctx, "destination", "instance", app, "mem", "vmstate", "lease")
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := <-rpc.receipt; got.WakeID != app.WakeID || got.AppID != app.AppID || got.DeploymentID != app.DeploymentID || got.InstanceID != "instance" {
			t.Fatalf("retained VM identity=%+v", got)
		}
	}
}
