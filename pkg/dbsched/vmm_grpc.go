package dbsched

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	// Deleting a database deletes its archive with the volume.
	_, err = c.DeleteDatabaseVolume(ctx, &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id, Archive: true})
	return err
}

// DropArchive deletes only the database's archive, through node's
// dbbackupd.
func (g *GRPCVMM) DropArchive(ctx context.Context, node Node, id string) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.DeleteDatabaseVolume(ctx, &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id, Archive: true, ArchiveOnly: true})
	return err
}

// ListVolumes reports node's database volumes.
func (g *GRPCVMM) ListVolumes(ctx context.Context, node Node) ([]NodeVolume, error) {
	c, err := g.client(ctx, node)
	if err != nil {
		return nil, err
	}
	resp, err := c.ListDatabaseVolumes(ctx, &vmmdpb.ListDatabaseVolumesRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]NodeVolume, 0, len(resp.GetVolumes()))
	for _, v := range resp.GetVolumes() {
		out = append(out, NodeVolume{DatabaseID: v.GetDatabaseId(), Running: v.GetRunning()})
	}
	return out, nil
}

// Start boots the VM; a VM that is already running counts as started.
func (g *GRPCVMM) Start(ctx context.Context, node Node, vm VM) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	req := &vmmdpb.StartDatabaseRequest{
		DatabaseId: vm.DatabaseID, PostgresMajor: int32(vm.Major), VcpuCount: int32(vm.VcpuCount), MemSizeMib: int32(vm.MemMiB),
		RestoreWindowSeconds: vm.RestoreWindowSeconds,
	}
	// Only the first boot of a restored database recovers; once
	// bootstrapped it boots from its own volume.
	if vm.RestoreSource != "" && !vm.Bootstrapped {
		req.RestoreSource, req.RestoreTarget = vm.RestoreSource, timestamppb.New(vm.RestoreTarget)
		req.RestoreUntilEnd = vm.RestoreUntilEnd
	}
	_, err = c.StartDatabase(ctx, req)
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
	in := &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: string(req.Op), Sql: req.SQL}
	if !req.After.IsZero() {
		in.After = timestamppb.New(req.After)
	}
	resp, err := c.DatabaseControl(ctx, in)
	if status.Code(err) == codes.FailedPrecondition {
		return dbguest.Response{}, fmt.Errorf("%w: %w", ErrVMNotRunning, err)
	}
	if err != nil {
		return dbguest.Response{}, err
	}
	out := dbguest.Response{State: dbguest.State(resp.GetState()), Major: int(resp.GetPostgresMajor()), Error: resp.GetErrorCode(), SQLState: resp.GetSqlstate(), Backup: resp.GetBackup()}
	if resp.GetRecoveredTo() != nil {
		out.RecoveredTo = resp.GetRecoveredTo().AsTime()
	}
	return out, nil
}
