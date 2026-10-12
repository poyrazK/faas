package dbsched

import (
	"context"
	"crypto/tls"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/overlay"
)

// DialFunc opens a gRPC connection to a database node's vmmd.
type DialFunc func(ctx context.Context, target string, tlsCfg *tls.Config) (*grpc.ClientConn, error)

// GRPCVMM drives each database node's vmmd over gRPC. It dials lazily and
// keeps one connection per target.
type GRPCVMM struct {
	tls   *tls.Config
	dial  DialFunc
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

// NewGRPCVMM dials vmmd through the overlay like schedd's app VM router.
// tlsCfg is schedd's vmmd client mTLS config; nil suits unix sockets.
func NewGRPCVMM(tlsCfg *tls.Config) *GRPCVMM {
	return NewGRPCVMMWithDial(tlsCfg, func(ctx context.Context, target string, tlsCfg *tls.Config) (*grpc.ClientConn, error) {
		return overlay.Dial(ctx, overlay.New(target), tlsCfg)
	})
}

// NewGRPCVMMWithDial is NewGRPCVMM with an injected dialer.
func NewGRPCVMMWithDial(tlsCfg *tls.Config, dial DialFunc) *GRPCVMM {
	return &GRPCVMM{tls: tlsCfg, dial: dial, conns: map[string]*grpc.ClientConn{}}
}

var _ VMM = (*GRPCVMM)(nil)

func (g *GRPCVMM) client(ctx context.Context, node Node) (vmmdpb.VmmdClient, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	conn, ok := g.conns[node.TargetURL]
	if !ok {
		var err error
		if conn, err = g.dial(ctx, node.TargetURL, g.tls); err != nil {
			return nil, err
		}
		g.conns[node.TargetURL] = conn
	}
	return vmmdpb.NewVmmdClient(conn), nil
}

// Close releases every connection.
func (g *GRPCVMM) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for target, conn := range g.conns {
		_ = conn.Close()
		delete(g.conns, target)
	}
	return nil
}

// tolerate turns the given codes into success, so a retried step converges.
func tolerate(err error, ok ...codes.Code) error {
	code := status.Code(err)
	for _, c := range ok {
		if code == c {
			return nil
		}
	}
	return err
}

func (g *GRPCVMM) CreateVolume(ctx context.Context, node Node, id string, sizeBytes int64) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.CreateDatabaseVolume(ctx, &vmmdpb.CreateDatabaseVolumeRequest{DatabaseId: id, SizeBytes: sizeBytes})
	return tolerate(err, codes.AlreadyExists)
}

func (g *GRPCVMM) DeleteVolume(ctx context.Context, node Node, id string) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.DeleteDatabaseVolume(ctx, &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id})
	return err
}

// Start boots the VM; a VM that is already running counts as started.
func (g *GRPCVMM) Start(ctx context.Context, node Node, vm VM) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.StartDatabase(ctx, &vmmdpb.StartDatabaseRequest{
		DatabaseId: vm.DatabaseID, PostgresMajor: int32(vm.Major), VcpuCount: int32(vm.VcpuCount), MemSizeMib: int32(vm.MemMiB),
	})
	return tolerate(err, codes.FailedPrecondition)
}

// Stop parks the VM; a VM that is not running counts as stopped.
func (g *GRPCVMM) Stop(ctx context.Context, node Node, id string) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.StopDatabase(ctx, &vmmdpb.StopDatabaseRequest{DatabaseId: id})
	return tolerate(err, codes.FailedPrecondition)
}

func (g *GRPCVMM) Control(ctx context.Context, node Node, id string, req dbguest.Request) (dbguest.Response, error) {
	c, err := g.client(ctx, node)
	if err != nil {
		return dbguest.Response{}, err
	}
	resp, err := c.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: string(req.Op), Sql: req.SQL})
	if err != nil {
		return dbguest.Response{}, err
	}
	return dbguest.Response{State: dbguest.State(resp.GetState()), Major: int(resp.GetPostgresMajor()), Error: resp.GetErrorCode(), SQLState: resp.GetSqlstate()}, nil
}
