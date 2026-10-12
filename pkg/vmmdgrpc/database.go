package vmmdgrpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// Database VM bounds accepted over RPC. Plan and class limits are enforced
// by the caller; these reject nonsense before a VM is booted.
const (
	maxDatabaseVcpus  = 32
	minDatabaseMemMiB = 128
	maxDatabaseMemMiB = 65536
)

// DatabaseRuntime is vmmd's database VM runtime (ADR-1027);
// *dbvm.Runtime satisfies it.
type DatabaseRuntime interface {
	CreateVolume(ctx context.Context, id string, sizeBytes int64) error
	DeleteVolume(ctx context.Context, id string) error
	Start(ctx context.Context, req dbvm.BootRequest) error
	Stop(ctx context.Context, id string) error
	Control(ctx context.Context, id string, req dbguest.Request) (dbguest.Response, error)
}

// WithDatabases enables the database VM RPCs on a database node.
func (s *Server) WithDatabases(rt DatabaseRuntime) *Server {
	s.databases = rt
	return s
}

func (s *Server) databaseRuntime() (DatabaseRuntime, error) {
	if s.databases == nil {
		return nil, status.Error(codes.Unimplemented, "database VMs are not enabled on this node")
	}
	return s.databases, nil
}

func (s *Server) CreateDatabaseVolume(ctx context.Context, req *vmmdpb.CreateDatabaseVolumeRequest) (*vmmdpb.CreateDatabaseVolumeResponse, error) {
	rt, err := s.databaseRuntime()
	if err != nil {
		return nil, err
	}
	if req.GetSizeBytes() < dbvm.MinVolumeBytes || req.GetSizeBytes() > dbvm.MaxVolumeBytes {
		return nil, status.Error(codes.InvalidArgument, "volume size out of range")
	}
	if err := rt.CreateVolume(ctx, req.GetDatabaseId(), req.GetSizeBytes()); err != nil {
		return nil, s.databaseStatus("create volume", err)
	}
	return &vmmdpb.CreateDatabaseVolumeResponse{}, nil
}

func (s *Server) DeleteDatabaseVolume(ctx context.Context, req *vmmdpb.DeleteDatabaseVolumeRequest) (*vmmdpb.DeleteDatabaseVolumeResponse, error) {
	rt, err := s.databaseRuntime()
	if err != nil {
		return nil, err
	}
	if err := rt.DeleteVolume(ctx, req.GetDatabaseId()); err != nil {
		return nil, s.databaseStatus("delete volume", err)
	}
	return &vmmdpb.DeleteDatabaseVolumeResponse{}, nil
}

func (s *Server) StartDatabase(ctx context.Context, req *vmmdpb.StartDatabaseRequest) (*vmmdpb.StartDatabaseResponse, error) {
	rt, err := s.databaseRuntime()
	if err != nil {
		return nil, err
	}
	boot, err := databaseBootRequest(req)
	if err != nil {
		return nil, err
	}
	if err := rt.Start(ctx, boot); err != nil {
		return nil, s.databaseStatus("start", err)
	}
	resp, err := rt.Control(ctx, boot.ID, dbguest.Request{Op: dbguest.OpStatus})
	if err != nil {
		return nil, s.databaseStatus("status after start", err)
	}
	return &vmmdpb.StartDatabaseResponse{State: string(resp.State)}, nil
}

func databaseBootRequest(req *vmmdpb.StartDatabaseRequest) (dbvm.BootRequest, error) {
	major := int(req.GetPostgresMajor())
	vcpus := int(req.GetVcpuCount())
	mem := int(req.GetMemSizeMib())
	if major < dbguest.MinMajor || major > dbguest.MaxMajor || vcpus < 1 || vcpus > maxDatabaseVcpus ||
		mem < minDatabaseMemMiB || mem > maxDatabaseMemMiB || req.GetVolumeBandwidthBytesPerSec() < 0 || req.GetVolumeOpsPerSec() < 0 {
		return dbvm.BootRequest{}, status.Error(codes.InvalidArgument, "database VM shape out of range")
	}
	return dbvm.BootRequest{
		ID: req.GetDatabaseId(), Major: major, VcpuCount: vcpus, MemSizeMiB: mem,
		VolumeRateLimiter: volumeRateLimiter(req.GetVolumeBandwidthBytesPerSec(), req.GetVolumeOpsPerSec()),
	}, nil
}

// volumeRateLimiter turns per-second ceilings into Firecracker token
// buckets refilled every second. Zero leaves that dimension unlimited.
func volumeRateLimiter(bytesPerSec, opsPerSec int64) *fcvm.RateLimiter {
	if bytesPerSec == 0 && opsPerSec == 0 {
		return nil
	}
	rl := &fcvm.RateLimiter{}
	if bytesPerSec > 0 {
		rl.Bandwidth = &fcvm.TokenBucket{Size: bytesPerSec, RefillTimeMs: 1000}
	}
	if opsPerSec > 0 {
		rl.Ops = &fcvm.TokenBucket{Size: opsPerSec, RefillTimeMs: 1000}
	}
	return rl
}

func (s *Server) StopDatabase(ctx context.Context, req *vmmdpb.StopDatabaseRequest) (*vmmdpb.StopDatabaseResponse, error) {
	rt, err := s.databaseRuntime()
	if err != nil {
		return nil, err
	}
	if err := rt.Stop(ctx, req.GetDatabaseId()); err != nil {
		return nil, s.databaseStatus("stop", err)
	}
	return &vmmdpb.StopDatabaseResponse{}, nil
}

// DatabaseControl relays status, bootstrap and admin SQL. Stop goes through
// StopDatabase so the jail is always torn down with the VM. The SQL can
// carry role passwords and is never logged.
func (s *Server) DatabaseControl(ctx context.Context, req *vmmdpb.DatabaseControlRequest) (*vmmdpb.DatabaseControlResponse, error) {
	rt, err := s.databaseRuntime()
	if err != nil {
		return nil, err
	}
	control := dbguest.Request{Op: dbguest.Op(req.GetOp()), SQL: req.GetSql()}
	if control.Op == dbguest.OpStop {
		return nil, status.Error(codes.InvalidArgument, "stop a database with StopDatabase")
	}
	if err := control.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid database control request")
	}
	resp, err := rt.Control(ctx, req.GetDatabaseId(), control)
	if err != nil {
		return nil, s.databaseStatus("control", err)
	}
	return &vmmdpb.DatabaseControlResponse{
		State: string(resp.State), PostgresMajor: int32(resp.Major), ErrorCode: resp.Error, Sqlstate: resp.SQLState,
	}, nil
}

// databaseStatus maps runtime errors to stable gRPC codes and logs
// unexpected failures. Runtime errors never carry request secrets.
func (s *Server) databaseStatus(op string, err error) error {
	code := databaseCode(err)
	if code == codes.Internal && s.log != nil {
		s.log.Error("vmmd: database rpc failed", "op", op, "err", err)
	}
	if code == codes.Internal || code == codes.DeadlineExceeded {
		return status.Errorf(code, "database %s failed", op)
	}
	return status.Errorf(code, "database %s: %v", op, err)
}

func databaseCode(err error) codes.Code {
	switch {
	case errors.Is(err, dbvm.ErrInvalidID), errors.Is(err, dbvm.ErrUnknownMajor):
		return codes.InvalidArgument
	case errors.Is(err, dbvm.ErrVolumeExists):
		return codes.AlreadyExists
	case errors.Is(err, dbvm.ErrVolumeNotFound):
		return codes.NotFound
	case errors.Is(err, dbvm.ErrRunning), errors.Is(err, dbvm.ErrNotRunning):
		return codes.FailedPrecondition
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return codes.DeadlineExceeded
	default:
		return codes.Internal
	}
}
