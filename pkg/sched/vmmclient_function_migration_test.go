package sched_test

// adr: 1575. An older empty cancel response must never publish RUNNING.

import (
	"context"
	"errors"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type functionMigrationResponseNode struct {
	vmmdpb.UnimplementedVmmdServer
	response *vmmdpb.CancelLiveMigrationResponse
}

func (s *functionMigrationResponseNode) Ping(context.Context, *vmmdpb.PingRequest) (*vmmdpb.PingResponse, error) {
	return &vmmdpb.PingResponse{FcVersion: "1.10.0", SupportsSecretAliases: true}, nil
}

func (s *functionMigrationResponseNode) CancelLiveMigration(context.Context, *vmmdpb.CancelLiveMigrationRequest) (*vmmdpb.CancelLiveMigrationResponse, error) {
	return s.response, nil
}

func TestFunctionMigrationClientRequiresExplicitSourceProtocol(t *testing.T) {
	for _, version := range []uint32{0, fcvm.FunctionMigrationSourceProtocolVersion, 2} {
		for _, retired := range []bool{false, true} {
			node := &functionMigrationResponseNode{response: &vmmdpb.CancelLiveMigrationResponse{SourceProtocolVersion: version, SourceRetired: retired}}
			server := grpc.NewServer()
			vmmdpb.RegisterVmmdServer(server, node)
			listener := bufconn.Listen(1024 * 1024)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			conn, err := wire.DialContext(t.Context(), "unix:///function-migration", nil, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close(); _ = listener.Close() })
			actual, err := sched.NewVMMClient(conn).CancelFunctionMigrationSource(t.Context(), "source", "instance", "token")
			if version != fcvm.FunctionMigrationSourceProtocolVersion {
				if !errors.Is(err, state.ErrMigrationCommitUnresolved) {
					t.Fatal("accepted legacy/unknown source response", version, err)
				}
			} else if err != nil || actual != retired {
				t.Fatal("lost physical outcome over gRPC", actual, retired, err)
			}
		}
	}
}
