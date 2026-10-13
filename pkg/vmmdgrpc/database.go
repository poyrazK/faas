package vmmdgrpc

import (
	"context"
	"errors"
	"net"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	// DropArchive deletes a deleted database's archive.
	DropArchive(ctx context.Context, id string) error
	ListVolumes() ([]dbvm.Volume, error)
	Start(ctx context.Context, req dbvm.BootRequest) error
	Stop(ctx context.Context, id string) error
	Control(ctx context.Context, id string, req dbguest.Request) (dbguest.Response, error)
	// Dial opens a stream to the VM's PostgreSQL data port only.
	Dial(ctx context.Context, id string) (net.Conn, error)
}

// Platform client identities allowed on the database RPCs.
const (
	databaseCallerSchedd  = "schedd.faas"
	databaseCallerPgproxy = "pgproxyd.faas"
)

// requireDatabaseCaller admits an mTLS client whose certificate names one
// of allowed. A caller without TLS reached vmmd's unix socket, whose
// permissions are the boundary there.
func requireDatabaseCaller(ctx context.Context, allowed ...string) error {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return status.Error(codes.PermissionDenied, "database rpc: no peer")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil
	}
	certs := tlsInfo.State.PeerCertificates
	if len(certs) == 0 || !slices.Contains(allowed, certs[0].Subject.CommonName) {
		return status.Error(codes.PermissionDenied, "database rpc: caller not allowed")
	}
	return nil
}

// WithDatabases enables the database VM RPCs on a database node.
func (s *Server) WithDatabases(rt DatabaseRuntime) *Server {
	s.databases = rt
	return s
}

func (s *Server) databaseRuntime(ctx context.Context, allowed ...string) (DatabaseRuntime, error) {
	if s.databases == nil {
		return nil, status.Error(codes.Unimplemented, "database VMs are not enabled on this node")
	}
	if err := requireDatabaseCaller(ctx, allowed...); err != nil {
		return nil, err
	}
	return s.databases, nil
}

func (s *Server) CreateDatabaseVolume(ctx context.Context, req *vmmdpb.CreateDatabaseVolumeRequest) (*vmmdpb.CreateDatabaseVolumeResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
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
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	// The archive goes first: if dbbackupd is unreachable the volume stays
	// too, and schedd's retried delete finds both.
	if req.GetArchive() || req.GetArchiveOnly() {
		if err := rt.DropArchive(ctx, req.GetDatabaseId()); err != nil {
			return nil, s.databaseStatus("drop archive", err)
		}
	}
	if req.GetArchiveOnly() {
		return &vmmdpb.DeleteDatabaseVolumeResponse{}, nil
	}
	if err := rt.DeleteVolume(ctx, req.GetDatabaseId()); err != nil {
		return nil, s.databaseStatus("delete volume", err)
	}
	return &vmmdpb.DeleteDatabaseVolumeResponse{}, nil
}

// ListDatabaseVolumes reports the node's database volumes to schedd.
func (s *Server) ListDatabaseVolumes(ctx context.Context, _ *vmmdpb.ListDatabaseVolumesRequest) (*vmmdpb.ListDatabaseVolumesResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	volumes, err := rt.ListVolumes()
	if err != nil {
		return nil, s.databaseStatus("list volumes", err)
	}
	out := &vmmdpb.ListDatabaseVolumesResponse{}
	for _, v := range volumes {
		out.Volumes = append(out.Volumes, &vmmdpb.DatabaseVolume{DatabaseId: v.ID, Running: v.Running})
	}
	return out, nil
}

func (s *Server) StartDatabase(ctx context.Context, req *vmmdpb.StartDatabaseRequest) (*vmmdpb.StartDatabaseResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
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
	boot := dbvm.BootRequest{
		ID: req.GetDatabaseId(), Major: major, VcpuCount: vcpus, MemSizeMiB: mem,
		VolumeRateLimiter:    volumeRateLimiter(req.GetVolumeBandwidthBytesPerSec(), req.GetVolumeOpsPerSec()),
		RestoreSource:        req.GetRestoreSource(),
		RestoreWindowSeconds: req.GetRestoreWindowSeconds(),
		RestoreUntilEnd:      req.GetRestoreUntilEnd(),
	}
	if req.GetRestoreTarget() != nil {
		if err := req.GetRestoreTarget().CheckValid(); err != nil {
			return dbvm.BootRequest{}, status.Error(codes.InvalidArgument, "invalid restore target")
		}
		boot.RestoreTarget = req.GetRestoreTarget().AsTime()
	}
	if boot.RestoreWindowSeconds < 0 {
		return dbvm.BootRequest{}, status.Error(codes.InvalidArgument, "negative restore window")
	}
	if (boot.RestoreSource == "") != boot.RestoreTarget.IsZero() || (boot.RestoreUntilEnd && boot.RestoreSource == "") {
		return dbvm.BootRequest{}, status.Error(codes.InvalidArgument, "a restore names its source and target")
	}
	return boot, nil
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
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
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
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	control := dbguest.Request{Op: dbguest.Op(req.GetOp()), SQL: req.GetSql()}
	if req.GetAfter() != nil {
		control.After = req.GetAfter().AsTime()
	}
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
	out := &vmmdpb.DatabaseControlResponse{
		State: string(resp.State), PostgresMajor: int32(resp.Major), ErrorCode: resp.Error, Sqlstate: resp.SQLState, Backup: resp.Backup,
	}
	if !resp.RecoveredTo.IsZero() {
		out.RecoveredTo = timestamppb.New(resp.RecoveredTo)
	}
	return out, nil
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

// DatabaseSession relays one client session between pgproxyd and the
// database VM's data port. Either side ending ends the session.
func (s *Server) DatabaseSession(stream vmmdpb.Vmmd_DatabaseSessionServer) error {
	ctx := stream.Context()
	rt, err := s.databaseRuntime(ctx, databaseCallerPgproxy)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	conn, err := rt.Dial(ctx, first.GetDatabaseId())
	if err != nil {
		return s.databaseStatus("session", err)
	}
	defer func() { _ = conn.Close() }()
	if data := first.GetData(); len(data) > 0 {
		if _, err := conn.Write(data); err != nil {
			return nil
		}
	}
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 32<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if stream.Send(&vmmdpb.DatabaseSessionFrame{Data: append([]byte(nil), buf[:n]...)}) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			frame, err := stream.Recv()
			if err != nil {
				return
			}
			if _, err := conn.Write(frame.GetData()); err != nil {
				return
			}
		}
	}()
	<-done
	return nil
}
