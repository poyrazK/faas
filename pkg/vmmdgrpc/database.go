package vmmdgrpc

import (
	"context"
	"errors"
	"net"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbarchive"
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
	GrowVolume(ctx context.Context, id string, sizeBytes int64) error
	PruneArchive(ctx context.Context, id string, windowSeconds int64) (dbarchive.PruneResult, error)
	ArchiveBytes(ctx context.Context, ids []string) (map[string]int64, error)
	// PutArchiveHold, GetArchiveHold and DeleteArchiveHold manage the
	// holds that keep a database's archive restorable to a point
	// (ADR-1027 snapshots).
	PutArchiveHold(ctx context.Context, id, name string, point time.Time) (dbarchive.Hold, error)
	GetArchiveHold(ctx context.Context, id, name string) (dbarchive.Hold, error)
	DeleteArchiveHold(ctx context.Context, id, name string) error
	// ExportVolume and ImportVolume carry a stopped database's volume to
	// another node; SyncArchive and ReleaseArchive hand its archive copy
	// to the bucket (ADR-1027 moves).
	ExportVolume(id string) (*dbvm.VolumeExport, error)
	ImportVolume(id string, sizeBytes int64) (*dbvm.VolumeImport, error)
	SyncArchive(ctx context.Context, id string) (int64, error)
	ReleaseArchive(ctx context.Context, id string) error
	Start(ctx context.Context, req dbvm.BootRequest) error
	// Stale reports a running VM on an outdated base or launch.
	Stale(id string) bool
	// SetStandbyNode and StandbyNodes keep a primary's standby node in its
	// VM's record, which outlives vmmd (ADR-1027).
	SetStandbyNode(id, node string) error
	StandbyNodes() map[string]string
	Stop(ctx context.Context, id string) error
	Control(ctx context.Context, id string, req dbguest.Request) (dbguest.Response, error)
	// Dial opens a stream to the VM's PostgreSQL data port only.
	Dial(ctx context.Context, id string) (net.Conn, error)
	// DialReplication opens a standby's replication stream to a running
	// primary's guest (ADR-1027 production class).
	DialReplication(ctx context.Context, id string) (net.Conn, error)
	// SetImportSource lets a running database's import reach src.
	SetImportSource(id string, src dbvm.ImportSource) error
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
	// Primaries that outlived the last vmmd still admit their standbys.
	s.databasePeers.restore(rt.StandbyNodes())
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
	if req.GetReleaseArchive() {
		if req.GetArchive() || req.GetArchiveOnly() {
			return nil, status.Error(codes.InvalidArgument, "release_archive excludes archive and archive_only")
		}
		// A retried release finds the volume gone and releases again.
		if err := rt.DeleteVolume(ctx, req.GetDatabaseId()); err != nil {
			return nil, s.databaseStatus("delete volume", err)
		}
		if err := rt.ReleaseArchive(ctx, req.GetDatabaseId()); err != nil {
			return nil, s.databaseStatus("release archive", err)
		}
		return &vmmdpb.DeleteDatabaseVolumeResponse{}, nil
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

// GrowDatabaseVolume enlarges a stopped database's volume for schedd.
func (s *Server) GrowDatabaseVolume(ctx context.Context, req *vmmdpb.GrowDatabaseVolumeRequest) (*vmmdpb.GrowDatabaseVolumeResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	if err := rt.GrowVolume(ctx, req.GetDatabaseId(), req.GetSizeBytes()); err != nil {
		return nil, s.databaseStatus("grow volume", err)
	}
	return &vmmdpb.GrowDatabaseVolumeResponse{}, nil
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

// PruneDatabaseArchive prunes a lost node's database's archive in the
// shared bucket.
func (s *Server) PruneDatabaseArchive(ctx context.Context, req *vmmdpb.PruneDatabaseArchiveRequest) (*vmmdpb.PruneDatabaseArchiveResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	if req.GetRestoreWindowSeconds() < 0 {
		return nil, status.Error(codes.InvalidArgument, "negative restore window")
	}
	res, err := rt.PruneArchive(ctx, req.GetDatabaseId(), req.GetRestoreWindowSeconds())
	if err != nil {
		return nil, s.databaseStatus("prune archive", err)
	}
	return &vmmdpb.PruneDatabaseArchiveResponse{Backups: int32(res.Backups), Wal: int32(res.WAL)}, nil
}

// DatabaseArchiveBytes sizes the node's database archives for schedd.
func (s *Server) DatabaseArchiveBytes(ctx context.Context, req *vmmdpb.DatabaseArchiveBytesRequest) (*vmmdpb.DatabaseArchiveBytesResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	if len(req.GetDatabaseIds()) > maxArchiveSizeBatch {
		return nil, status.Error(codes.InvalidArgument, "too many databases")
	}
	sizes, err := rt.ArchiveBytes(ctx, req.GetDatabaseIds())
	if err != nil {
		return nil, s.databaseStatus("archive bytes", err)
	}
	return &vmmdpb.DatabaseArchiveBytesResponse{Bytes: sizes}, nil
}

// DatabaseArchiveHold places, reads or removes a hold in a database's
// archive for schedd's snapshots.
func (s *Server) DatabaseArchiveHold(ctx context.Context, req *vmmdpb.DatabaseArchiveHoldRequest) (*vmmdpb.DatabaseArchiveHoldResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	if !dbarchive.ValidHoldName(req.GetName()) {
		return nil, status.Error(codes.InvalidArgument, "invalid hold name")
	}
	var hold dbarchive.Hold
	switch req.GetAction() {
	case vmmdpb.DatabaseArchiveHoldAction_DATABASE_ARCHIVE_HOLD_ACTION_PUT:
		if req.GetPointInTime() == nil {
			return nil, status.Error(codes.InvalidArgument, "a hold needs a point in time")
		}
		hold, err = rt.PutArchiveHold(ctx, req.GetDatabaseId(), req.GetName(), req.GetPointInTime().AsTime())
	case vmmdpb.DatabaseArchiveHoldAction_DATABASE_ARCHIVE_HOLD_ACTION_GET:
		hold, err = rt.GetArchiveHold(ctx, req.GetDatabaseId(), req.GetName())
	case vmmdpb.DatabaseArchiveHoldAction_DATABASE_ARCHIVE_HOLD_ACTION_DELETE:
		if err := rt.DeleteArchiveHold(ctx, req.GetDatabaseId(), req.GetName()); err != nil {
			return nil, s.databaseStatus("remove archive hold", err)
		}
		return &vmmdpb.DatabaseArchiveHoldResponse{}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown hold action")
	}
	switch {
	case errors.Is(err, dbarchive.ErrNotFound):
		return nil, status.Error(codes.NotFound, "no such archive hold")
	case errors.Is(err, dbarchive.ErrConflict):
		return nil, status.Error(codes.AlreadyExists, "the archive hold pins another point")
	case err != nil:
		return nil, s.databaseStatus("archive hold", err)
	}
	return &vmmdpb.DatabaseArchiveHoldResponse{PointInTime: timestamppb.New(hold.PointInTime), CreatedAt: timestamppb.New(hold.CreatedAt)}, nil
}

// maxArchiveSizeBatch bounds one DatabaseArchiveBytes request.
const maxArchiveSizeBatch = 1000

func (s *Server) StartDatabase(ctx context.Context, req *vmmdpb.StartDatabaseRequest) (*vmmdpb.StartDatabaseResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	boot, err := databaseBootRequest(req)
	if err != nil {
		return nil, err
	}
	if req.GetPrimaryNode() != "" {
		if boot.Primary, err = s.databasePeers.dialer(req.GetDatabaseId(), req.GetPrimaryNode(), req.GetPrimaryTarget()); err != nil {
			return nil, err
		}
		boot.PrimaryNode, boot.PrimaryTarget = req.GetPrimaryNode(), req.GetPrimaryTarget()
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
	if (req.GetPrimaryNode() == "") != (req.GetPrimaryTarget() == "") || (req.GetPrimaryNode() != "" && boot.RestoreSource != "") {
		return dbvm.BootRequest{}, status.Error(codes.InvalidArgument, "a standby names its primary's node and target and is not a restore")
	}
	if req.GetPromote() && (req.GetPrimaryNode() != "" || boot.RestoreSource != "") {
		return dbvm.BootRequest{}, status.Error(codes.InvalidArgument, "a promotion boot has no primary and is not a restore")
	}
	boot.Promote = req.GetPromote()
	if from := int(req.GetUpgradeFromMajor()); from != 0 {
		if from < dbguest.MinMajor || from >= major || req.GetPrimaryNode() != "" || boot.Promote || boot.RestoreSource != "" {
			return dbvm.BootRequest{}, status.Error(codes.InvalidArgument, "an upgrade goes from an older supported major and is neither a standby, a promotion nor a restore")
		}
		boot.UpgradeFrom = from
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
	// A stopped primary has no standby to admit; the next start attaches
	// one again.
	s.databasePeers.forget(req.GetDatabaseId())
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
	control := dbguest.Request{Op: dbguest.Op(req.GetOp()), SQL: req.GetSql(), Replication: dbguest.ReplicationMode(req.GetReplication()), Drill: req.GetDrill()}
	if in := req.GetImport(); in != nil {
		control.Import = &dbguest.ImportSource{Database: in.GetDatabase(), User: in.GetUser(), Password: in.GetPassword(),
			TargetDatabase: in.GetTargetDatabase(), Owner: in.GetOwner(), SkipExtensions: in.GetSkipExtensions(), OperationID: in.GetOperationId()}
	}
	if req.GetAfter() != nil {
		control.After = req.GetAfter().AsTime()
	}
	if control.Op == dbguest.OpStop {
		return nil, status.Error(codes.InvalidArgument, "stop a database with StopDatabase")
	}
	if err := control.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid database control request")
	}
	standby, noted, err := s.databasePeers.noteStandby(req.GetDatabaseId(), control, req.GetStandbyNode())
	if err != nil {
		return nil, err
	}
	if noted {
		if err := rt.SetStandbyNode(req.GetDatabaseId(), standby); err != nil {
			return nil, s.databaseStatus("record standby", err)
		}
	}
	if in := req.GetImport(); in != nil {
		// The host side of the import: where the guest's connections go.
		if err := rt.SetImportSource(req.GetDatabaseId(), dbvm.ImportSource{Host: in.GetHost(), Port: int(in.GetPort())}); err != nil {
			return nil, s.databaseStatus("import source", err)
		}
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
	if !resp.MarkedAt.IsZero() {
		out.MarkedAt = timestamppb.New(resp.MarkedAt)
	}
	if st := resp.Storage; st != nil {
		out.Storage = &vmmdpb.DatabaseStorage{TotalBytes: st.TotalBytes, FreeBytes: st.FreeBytes, ReadOnly: st.ReadOnly, Low: st.Low}
	}
	if control.Op == dbguest.OpStatus {
		out.Stale = rt.Stale(req.GetDatabaseId())
	}
	if r := resp.Replication; r != nil {
		out.Replication = &vmmdpb.DatabaseReplicationStatus{Mode: string(r.Mode), Streaming: r.Streaming, Synchronous: r.Synchronous, LagBytes: r.LagBytes}
	}
	if i := resp.Import; i != nil {
		out.Import = &vmmdpb.DatabaseImportStatus{State: string(i.State), ErrorCode: i.Error, SourceMajor: int32(i.SourceMajor), OperationId: i.OperationID, //nolint:gosec // a PostgreSQL major
			MissingExtensions: dbguest.ReportableExtensions(i.MissingExtensions), RequiredVolumeBytes: max(i.RequiredVolumeBytes, 0)}
		if !i.StartedAt.IsZero() {
			out.Import.StartedAt = timestamppb.New(i.StartedAt)
		}
		if !i.FinishedAt.IsZero() {
			out.Import.FinishedAt = timestamppb.New(i.FinishedAt)
		}
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
	case errors.Is(err, dbvm.ErrInvalidID), errors.Is(err, dbvm.ErrUnknownMajor), errors.Is(err, dbvm.ErrVolumeShrink), errors.Is(err, dbvm.ErrInvalidImportSource):
		return codes.InvalidArgument
	case errors.Is(err, dbvm.ErrVolumeExists):
		return codes.AlreadyExists
	case errors.Is(err, dbvm.ErrVolumeNotFound):
		return codes.NotFound
	case errors.Is(err, dbvm.ErrRunning), errors.Is(err, dbvm.ErrNotRunning), errors.Is(err, dbvm.ErrNoArchive), errors.Is(err, dbvm.ErrTransferring):
		return codes.FailedPrecondition
	case errors.Is(err, dbvm.ErrTransferDigest):
		return codes.DataLoss
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
	spliceFrames(stream, conn, first.GetData())
	return nil
}

// frameStream is either side of a DatabaseSession or DatabaseReplication
// stream.
type frameStream interface {
	Send(*vmmdpb.DatabaseSessionFrame) error
	Recv() (*vmmdpb.DatabaseSessionFrame, error)
}

// spliceFrames relays between a frame stream and a VM connection, after
// writing first, until either side ends.
func spliceFrames(stream frameStream, conn net.Conn, first []byte) {
	if len(first) > 0 {
		if _, err := conn.Write(first); err != nil {
			return
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
}
