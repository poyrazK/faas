//go:build linux

package vmmdgrpc

// adr: 20261030230157 — a real round trip over the local data socket, the way
// gatewayd-internal dials it (unix, no TLS).

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/wire"
)

type pingOnlyVmmd struct{ vmmdpb.UnimplementedVmmdServer }

func (pingOnlyVmmd) Ping(context.Context, *vmmdpb.PingRequest) (*vmmdpb.PingResponse, error) {
	return &vmmdpb.PingResponse{}, nil
}

// localDataClient serves a local data socket that admits uid and returns a
// client dialed like the gateway dials it.
func localDataClient(t *testing.T, uid int) vmmdpb.VmmdClient {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vmmd-data.sock")
	lis, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(LocalDataServerOptions(uid)...)
	vmmdpb.RegisterVmmdServer(srv, pingOnlyVmmd{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := wire.DialContext(context.Background(), "unix://"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

func TestLocalDataSocketRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := localDataClient(t, os.Getuid())
	if _, err := client.Ping(ctx, &vmmdpb.PingRequest{}); err != nil {
		t.Fatalf("Ping from the admitted uid: %v", err)
	}
	if _, err := client.Destroy(ctx, &vmmdpb.DestroyRequest{Instance: "i-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Destroy over the local data socket: %v, want PermissionDenied", err)
	}

	stranger := localDataClient(t, os.Getuid()+1)
	if _, err := stranger.Ping(ctx, &vmmdpb.PingRequest{}); err == nil {
		t.Fatal("a uid other than the gateway's completed an RPC")
	}
}
