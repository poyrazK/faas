// adr: 066 — live migration adopts the instance on the destination node.
package sched_test

import (
	"context"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type adoptIdentityNode struct {
	vmmdpb.UnimplementedVmmdServer
	fields wire.CorrelationFields
}

func (s *adoptIdentityNode) Ping(context.Context, *vmmdpb.PingRequest) (*vmmdpb.PingResponse, error) {
	return &vmmdpb.PingResponse{FcVersion: "1.10.0", SupportsSecretAliases: true}, nil
}

func (s *adoptIdentityNode) AdoptMigratedInstance(ctx context.Context, _ *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	s.fields, _ = wire.CorrelationFromIncoming(ctx)
	return &vmmdpb.AdoptMigratedInstanceResponse{}, nil
}

// The destination boot emits wake timeline events, which vmmd drops without
// the app identity: every migration lost its readiness and boot rows (H5-18).
func TestAdoptMigratedInstanceCarriesAppIdentity(t *testing.T) {
	node := &adoptIdentityNode{}
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, node)
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///adopt-identity", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); _ = listener.Close() })
	client := sched.NewVMMClient(conn)
	app := sched.AppSpec{Plan: api.PlanPro, AppID: "app-1", DeploymentID: "dep-1"}
	if _, err := client.AdoptMigratedInstance(t.Context(), "source", "instance-1", app, "mem", "state", "lease"); err != nil {
		t.Fatal(err)
	}
	if node.fields.AppID != "app-1" || node.fields.DeploymentID != "dep-1" || node.fields.InstanceID != "instance-1" {
		t.Fatalf("adopt correlation = %+v, want the app, deployment and instance", node.fields)
	}
}
