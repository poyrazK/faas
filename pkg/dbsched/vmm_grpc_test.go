// adr: 1027
package dbsched

import (
	"context"
	"crypto/tls"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

// scriptedVmmd answers the database RPCs with fixed errors so the adapter's
// idempotence mapping can be checked over a real gRPC connection.
type scriptedVmmd struct {
	vmmdpb.UnimplementedVmmdServer
	createErr, startErr, stopErr error
	lastControl                  *vmmdpb.DatabaseControlRequest
}

func (s *scriptedVmmd) CreateDatabaseVolume(context.Context, *vmmdpb.CreateDatabaseVolumeRequest) (*vmmdpb.CreateDatabaseVolumeResponse, error) {
	return &vmmdpb.CreateDatabaseVolumeResponse{}, s.createErr
}
func (s *scriptedVmmd) StartDatabase(context.Context, *vmmdpb.StartDatabaseRequest) (*vmmdpb.StartDatabaseResponse, error) {
	return &vmmdpb.StartDatabaseResponse{State: "ready"}, s.startErr
}
func (s *scriptedVmmd) StopDatabase(context.Context, *vmmdpb.StopDatabaseRequest) (*vmmdpb.StopDatabaseResponse, error) {
	return &vmmdpb.StopDatabaseResponse{}, s.stopErr
}
func (s *scriptedVmmd) DatabaseControl(_ context.Context, req *vmmdpb.DatabaseControlRequest) (*vmmdpb.DatabaseControlResponse, error) {
	s.lastControl = req
	return &vmmdpb.DatabaseControlResponse{State: "ready", PostgresMajor: 17, ErrorCode: dbguest.ErrCodeAdminSQLFailed, Sqlstate: "42710"}, nil
}

func scriptedAdapter(t *testing.T, srv *scriptedVmmd) *GRPCVMM {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gsrv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(gsrv, srv)
	go func() { _ = gsrv.Serve(lis) }()
	t.Cleanup(gsrv.Stop)
	dials := 0
	g := NewGRPCVMMWithDial(nil, func(context.Context, string, *tls.Config) (*grpc.ClientConn, error) {
		dials++
		return grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() {
		_ = g.Close()
		if dials != 1 {
			t.Errorf("dialed %d times; connections must be reused per target", dials)
		}
	})
	return g
}

func TestGRPCVMMToleratesRetryOutcomes(t *testing.T) {
	srv := &scriptedVmmd{
		createErr: status.Error(codes.AlreadyExists, "exists"),
		startErr:  status.Error(codes.FailedPrecondition, "running"),
		stopErr:   status.Error(codes.FailedPrecondition, "not running"),
	}
	g, ctx, node := scriptedAdapter(t, srv), context.Background(), Node{ID: "db-a", TargetURL: "unix:///run/faas/vmmd.sock"}
	if err := g.CreateVolume(ctx, node, "id", 1<<30); err != nil {
		t.Fatalf("existing volume must count as created: %v", err)
	}
	if err := g.Start(ctx, node, VM{DatabaseID: "id", Major: 17, VcpuCount: 1, MemMiB: 384}); err != nil {
		t.Fatalf("running VM must count as started: %v", err)
	}
	if err := g.Stop(ctx, node, "id"); err != nil {
		t.Fatalf("stopped VM must count as stopped: %v", err)
	}
	srv.startErr = status.Error(codes.Internal, "boom")
	if err := g.Start(ctx, node, VM{DatabaseID: "id", Major: 17, VcpuCount: 1, MemMiB: 384}); status.Code(err) != codes.Internal {
		t.Fatalf("real failures must surface: %v", err)
	}
	resp, err := g.Control(ctx, node, "id", dbguest.Request{Op: dbguest.OpAdminSQL, SQL: "CREATE ROLE app"})
	if err != nil || resp.SQLState != "42710" || resp.Error != dbguest.ErrCodeAdminSQLFailed || resp.Major != 17 {
		t.Fatalf("control = %+v, %v", resp, err)
	}
	if srv.lastControl.GetOp() != "admin_sql" || srv.lastControl.GetSql() != "CREATE ROLE app" {
		t.Fatalf("control request = %+v", srv.lastControl)
	}
}
