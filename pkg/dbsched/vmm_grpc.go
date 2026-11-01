package dbsched

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

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

// PruneArchive prunes a database's bucket archive through node's
// dbbackupd.
func (g *GRPCVMM) PruneArchive(ctx context.Context, node Node, id string, windowSeconds int64) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.PruneDatabaseArchive(ctx, &vmmdpb.PruneDatabaseArchiveRequest{DatabaseId: id, RestoreWindowSeconds: windowSeconds})
	return err
}

// PutHold places a hold on a database's archive through node's dbbackupd.
func (g *GRPCVMM) PutHold(ctx context.Context, node Node, id, name string, point time.Time) (ArchiveHold, error) {
	return g.hold(ctx, node, &vmmdpb.DatabaseArchiveHoldRequest{DatabaseId: id, Name: name,
		Action: vmmdpb.DatabaseArchiveHoldAction_DATABASE_ARCHIVE_HOLD_ACTION_PUT, PointInTime: timestamppb.New(point)})
}

// GetHold reads a hold on a database's archive through node's dbbackupd.
func (g *GRPCVMM) GetHold(ctx context.Context, node Node, id, name string) (ArchiveHold, error) {
	return g.hold(ctx, node, &vmmdpb.DatabaseArchiveHoldRequest{DatabaseId: id, Name: name,
		Action: vmmdpb.DatabaseArchiveHoldAction_DATABASE_ARCHIVE_HOLD_ACTION_GET})
}

// DeleteHold removes a hold on a database's archive through node's
// dbbackupd.
func (g *GRPCVMM) DeleteHold(ctx context.Context, node Node, id, name string) error {
	_, err := g.hold(ctx, node, &vmmdpb.DatabaseArchiveHoldRequest{DatabaseId: id, Name: name,
		Action: vmmdpb.DatabaseArchiveHoldAction_DATABASE_ARCHIVE_HOLD_ACTION_DELETE})
	return err
}

func (g *GRPCVMM) hold(ctx context.Context, node Node, req *vmmdpb.DatabaseArchiveHoldRequest) (ArchiveHold, error) {
	c, err := g.client(ctx, node)
	if err != nil {
		return ArchiveHold{}, err
	}
	resp, err := c.DatabaseArchiveHold(ctx, req)
	switch status.Code(err) {
	case codes.OK:
	case codes.NotFound:
		return ArchiveHold{}, fmt.Errorf("%w: %w", ErrNotFound, err)
	case codes.AlreadyExists:
		return ArchiveHold{}, fmt.Errorf("%w: %w", ErrConflict, err)
	default:
		return ArchiveHold{}, err
	}
	if req.GetAction() == vmmdpb.DatabaseArchiveHoldAction_DATABASE_ARCHIVE_HOLD_ACTION_DELETE {
		return ArchiveHold{}, nil
	}
	if resp.GetPointInTime() == nil || resp.GetCreatedAt() == nil {
		return ArchiveHold{}, errors.New("dbsched: hold response without its times")
	}
	return ArchiveHold{PointInTime: resp.GetPointInTime().AsTime(), CreatedAt: resp.GetCreatedAt().AsTime()}, nil
}

// ArchiveBytes sizes the databases' archives on node.
func (g *GRPCVMM) ArchiveBytes(ctx context.Context, node Node, ids []string) (map[string]int64, error) {
	c, err := g.client(ctx, node)
	if err != nil {
		return nil, err
	}
	resp, err := c.DatabaseArchiveBytes(ctx, &vmmdpb.DatabaseArchiveBytesRequest{DatabaseIds: ids})
	if err != nil {
		return nil, err
	}
	return resp.GetBytes(), nil
}

// GrowVolume enlarges a stopped database's volume on node.
func (g *GRPCVMM) GrowVolume(ctx context.Context, node Node, id string, sizeBytes int64) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.GrowDatabaseVolume(ctx, &vmmdpb.GrowDatabaseVolumeRequest{DatabaseId: id, SizeBytes: sizeBytes})
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
	req.Promote = vm.PromotePending
	req.UpgradeFromMajor = int32(vm.UpgradeFrom) //nolint:gosec // majors are 14-18
	_, err = c.StartDatabase(ctx, req)
	return tolerate(err, codes.FailedPrecondition)
}

// StartStandby boots the database's standby on node. Unlike Start it
// tolerates nothing: a standby left running is stopped first.
func (g *GRPCVMM) StartStandby(ctx context.Context, node, primary Node, vm VM) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.StartDatabase(ctx, &vmmdpb.StartDatabaseRequest{
		DatabaseId: vm.DatabaseID, PostgresMajor: int32(vm.Major), VcpuCount: int32(vm.VcpuCount), MemSizeMib: int32(vm.MemMiB),
		RestoreWindowSeconds: vm.RestoreWindowSeconds, PrimaryNode: primary.ID, PrimaryTarget: primary.TargetURL,
	})
	return err
}

// Replication sets and reports a running primary's replication.
func (g *GRPCVMM) Replication(ctx context.Context, node Node, id string, mode dbguest.ReplicationMode, standbyNode string) (dbguest.ReplicationStatus, error) {
	c, err := g.client(ctx, node)
	if err != nil {
		return dbguest.ReplicationStatus{}, err
	}
	resp, err := c.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: string(dbguest.OpReplication), Replication: string(mode), StandbyNode: standbyNode})
	if status.Code(err) == codes.FailedPrecondition {
		return dbguest.ReplicationStatus{}, fmt.Errorf("%w: %w", ErrVMNotRunning, err)
	}
	if err != nil {
		return dbguest.ReplicationStatus{}, err
	}
	if resp.GetErrorCode() != "" {
		return dbguest.ReplicationStatus{}, fmt.Errorf("guest error %s", resp.GetErrorCode())
	}
	r := resp.GetReplication()
	return dbguest.ReplicationStatus{Mode: dbguest.ReplicationMode(r.GetMode()), Streaming: r.GetStreaming(), Synchronous: r.GetSynchronous(), LagBytes: r.GetLagBytes()}, nil
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
	in := &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: string(req.Op), Sql: req.SQL, Drill: req.Drill}
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
	out := dbguest.Response{State: dbguest.State(resp.GetState()), Major: int(resp.GetPostgresMajor()), Error: resp.GetErrorCode(), SQLState: resp.GetSqlstate(), Backup: resp.GetBackup(),
		Stale: resp.GetStale()}
	if resp.GetRecoveredTo() != nil {
		out.RecoveredTo = resp.GetRecoveredTo().AsTime()
	}
	if resp.GetMarkedAt() != nil {
		out.MarkedAt = resp.GetMarkedAt().AsTime()
	}
	if resp.GetImport() != nil {
		status := importStatus(resp.GetImport())
		out.Import = &status
	}
	if st := resp.GetStorage(); st != nil {
		out.Storage = &dbguest.StorageStatus{TotalBytes: st.GetTotalBytes(), FreeBytes: st.GetFreeBytes(), ReadOnly: st.GetReadOnly(), Low: st.GetLow()}
	}
	return out, nil
}

// Import starts the database's import through its node's vmmd, which keeps
// the endpoint and forwards the login to the guest.
func (g *GRPCVMM) Import(ctx context.Context, node Node, id string, spec ImportSpec) (dbguest.ImportStatus, error) {
	c, err := g.client(ctx, node)
	if err != nil {
		return noImport, err
	}
	src := spec.Source
	resp, err := c.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: id, Op: string(dbguest.OpImport), Import: &vmmdpb.DatabaseImport{
		Host: spec.Host, Port: int32(spec.Port), Database: src.Database, User: src.User, Password: src.Password, //nolint:gosec // port validated
		TargetDatabase: src.TargetDatabase, Owner: src.Owner, SkipExtensions: src.SkipExtensions, OperationId: src.OperationID,
	}})
	if status.Code(err) == codes.FailedPrecondition {
		return noImport, fmt.Errorf("%w: %w", ErrVMNotRunning, err)
	}
	if err != nil {
		return noImport, err
	}
	if resp.GetErrorCode() == dbguest.ErrCodeImportOperationMismatch {
		return importStatus(resp.GetImport()), fmt.Errorf("%w: another import is running or done in this database", ErrConflict)
	}
	if resp.GetErrorCode() != "" {
		return noImport, fmt.Errorf("guest error %s", resp.GetErrorCode())
	}
	return importStatus(resp.GetImport()), nil
}

func importStatus(in *vmmdpb.DatabaseImportStatus) dbguest.ImportStatus {
	if in == nil {
		return noImport
	}
	out := dbguest.ImportStatus{State: dbguest.ImportState(in.GetState()), Error: in.GetErrorCode(), SourceMajor: int(in.GetSourceMajor()), OperationID: in.GetOperationId(),
		MissingExtensions: dbguest.ReportableExtensions(in.GetMissingExtensions()), RequiredVolumeBytes: max(in.GetRequiredVolumeBytes(), 0)}
	if in.GetStartedAt() != nil {
		out.StartedAt = in.GetStartedAt().AsTime()
	}
	if in.GetFinishedAt() != nil {
		out.FinishedAt = in.GetFinishedAt().AsTime()
	}
	return out
}

// CopyVolume relays one node's volume export into another's import. The
// importer checks the transfer digest, so a stream cut short or garbled
// installs nothing.
func (g *GRPCVMM) CopyVolume(ctx context.Context, from, to Node, id string) error {
	src, err := g.client(ctx, from)
	if err != nil {
		return err
	}
	dst, err := g.client(ctx, to)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out, err := src.ExportDatabaseVolume(ctx, &vmmdpb.ExportDatabaseVolumeRequest{DatabaseId: id})
	if err != nil {
		return fmt.Errorf("export from %s: %w", from.ID, err)
	}
	in, err := dst.ImportDatabaseVolume(ctx)
	if err != nil {
		return fmt.Errorf("import on %s: %w", to.ID, err)
	}
	for {
		chunk, err := out.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("export from %s: %w", from.ID, err)
		}
		if err := in.Send(chunk); err != nil {
			// The importer ended the stream; CloseAndRecv says why.
			break
		}
	}
	if _, err := in.CloseAndRecv(); err != nil {
		return fmt.Errorf("import on %s: %w", to.ID, err)
	}
	return nil
}

// SyncArchive ships node's copy of the database's archive to the bucket.
func (g *GRPCVMM) SyncArchive(ctx context.Context, node Node, id string) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.SyncDatabaseArchive(ctx, &vmmdpb.SyncDatabaseArchiveRequest{DatabaseId: id})
	return err
}

// DiscardVolume deletes only the volume; the archive is untouched.
func (g *GRPCVMM) DiscardVolume(ctx context.Context, node Node, id string) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.DeleteDatabaseVolume(ctx, &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id})
	return err
}

// ReleaseVolume deletes a moved database's old volume and releases node's
// copy of its archive to the bucket.
func (g *GRPCVMM) ReleaseVolume(ctx context.Context, node Node, id string) error {
	c, err := g.client(ctx, node)
	if err != nil {
		return err
	}
	_, err = c.DeleteDatabaseVolume(ctx, &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: id, ReleaseArchive: true})
	return err
}
