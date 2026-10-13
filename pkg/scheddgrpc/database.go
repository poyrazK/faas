package scheddgrpc

import (
	"context"
	"errors"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbsched"
)

// Platform client identities allowed on the database RPCs.
const (
	callerAPID    = "apid.faas"
	callerPgproxy = "pgproxyd.faas"
)

// DatabaseScheduler is schedd's database scheduler (ADR-1027);
// *dbsched.Scheduler satisfies it.
type DatabaseScheduler interface {
	Provision(ctx context.Context, req dbsched.ProvisionRequest) (dbsched.VM, error)
	Wake(ctx context.Context, id string) (dbsched.VM, error)
	Park(ctx context.Context, id string) error
	Touch(ctx context.Context, id string) error
	Admin(ctx context.Context, id, sql string) (dbguest.Response, error)
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (dbsched.VM, error)
	SetAlwaysOn(ctx context.Context, id string, alwaysOn bool) (dbsched.VM, error)
	Restore(ctx context.Context, req dbsched.RestoreRequest) (dbsched.VM, error)
	Usage(ctx context.Context, id string, from, to time.Time) (dbsched.Usage, error)
	RecordEgress(ctx context.Context, id string, bytes int64) error
	Resize(ctx context.Context, id string, shape dbsched.Shape) (dbsched.VM, error)
}

// WithDatabases enables the database RPCs.
func (s *Server) WithDatabases(d DatabaseScheduler) *Server {
	s.databases = d
	return s
}

// requireCaller admits an mTLS client whose certificate names one of
// allowed. A caller without TLS reached schedd's unix socket, whose
// permissions are the boundary there.
func requireCaller(ctx context.Context, allowed ...string) error {
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

func (s *Server) databaseScheduler(ctx context.Context, allowed ...string) (DatabaseScheduler, error) {
	if s.databases == nil {
		return nil, status.Error(codes.Unimplemented, "database scheduler is not enabled")
	}
	if err := requireCaller(ctx, allowed...); err != nil {
		return nil, err
	}
	return s.databases, nil
}

func databaseRuntime(vm dbsched.VM) *scheddpb.DatabaseRuntime {
	rt := &scheddpb.DatabaseRuntime{
		DatabaseId: vm.DatabaseID, NodeId: vm.NodeID, State: string(vm.State),
		Bootstrapped: vm.Bootstrapped, LastError: vm.LastError, VmmdTarget: vm.NodeTarget,
		PostgresMajor: int32(vm.Major), VcpuCount: int32(vm.VcpuCount), MemMib: int32(vm.MemMiB), VolumeBytes: vm.VolumeBytes,
		AlwaysOn: vm.AlwaysOn, RestoreWindowSeconds: vm.RestoreWindowSeconds, RestoreSourceId: vm.RestoreSource,
		ResizePending: vm.ResizePending,
	}
	if vm.RestoreSource != "" {
		rt.RestoreTarget = timestamppb.New(vm.RestoreTarget)
	}
	if !vm.RestoreRecoveredTo.IsZero() {
		rt.RestoreRecoveredTo = timestamppb.New(vm.RestoreRecoveredTo)
	}
	return rt
}

func provisionRequest(req *scheddpb.ProvisionDatabaseRequest) dbsched.ProvisionRequest {
	return dbsched.ProvisionRequest{
		DatabaseID: req.GetDatabaseId(), Major: int(req.GetPostgresMajor()), VcpuCount: int(req.GetVcpuCount()),
		MemMiB: int(req.GetMemMib()), VolumeBytes: req.GetVolumeBytes(), AlwaysOn: req.GetAlwaysOn(),
		RestoreWindowSeconds: req.GetRestoreWindowSeconds(),
	}
}

func (s *Server) ProvisionDatabase(ctx context.Context, req *scheddpb.ProvisionDatabaseRequest) (*scheddpb.DatabaseRuntime, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	pr := provisionRequest(req)
	if err := pr.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	vm, err := d.Provision(ctx, pr)
	if err != nil {
		return nil, s.databaseStatus("provision", err)
	}
	return databaseRuntime(vm), nil
}

// RestoreDatabase places a point-in-time restore; schedd runs it in the
// background and GetDatabase reports it provisioning until it is done.
func (s *Server) RestoreDatabase(ctx context.Context, req *scheddpb.RestoreDatabaseRequest) (*scheddpb.DatabaseRuntime, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	if req.GetDatabase() == nil || req.GetPointInTime().CheckValid() != nil {
		return nil, status.Error(codes.InvalidArgument, "restore needs a database and a point in time")
	}
	rr := dbsched.RestoreRequest{ProvisionRequest: provisionRequest(req.GetDatabase()), Source: req.GetSourceDatabaseId(), Target: req.GetPointInTime().AsTime()}
	if err := rr.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	vm, err := d.Restore(ctx, rr)
	if err != nil {
		return nil, s.databaseStatus("restore", err)
	}
	return databaseRuntime(vm), nil
}

func (s *Server) WakeDatabase(ctx context.Context, req *scheddpb.DatabaseRef) (*scheddpb.DatabaseRuntime, error) {
	d, err := s.databaseScheduler(ctx, callerAPID, callerPgproxy)
	if err != nil {
		return nil, err
	}
	vm, err := d.Wake(ctx, req.GetDatabaseId())
	if err != nil {
		return nil, s.databaseStatus("wake", err)
	}
	return databaseRuntime(vm), nil
}

func (s *Server) ParkDatabase(ctx context.Context, req *scheddpb.DatabaseRef) (*scheddpb.DatabaseRuntime, error) {
	d, err := s.databaseScheduler(ctx, callerAPID, callerPgproxy)
	if err != nil {
		return nil, err
	}
	if err := d.Park(ctx, req.GetDatabaseId()); err != nil {
		return nil, s.databaseStatus("park", err)
	}
	vm, err := d.Get(ctx, req.GetDatabaseId())
	if err != nil {
		return nil, s.databaseStatus("park", err)
	}
	return databaseRuntime(vm), nil
}

// TouchDatabase records activity and the egress pgproxyd counted since its
// last touch. The egress lands first: pgproxyd never resends it, and a
// database that stopped running still sent those bytes.
func (s *Server) TouchDatabase(ctx context.Context, req *scheddpb.TouchDatabaseRequest) (*scheddpb.TouchDatabaseResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID, callerPgproxy)
	if err != nil {
		return nil, err
	}
	if err := d.RecordEgress(ctx, req.GetDatabaseId(), req.GetEgressBytes()); err != nil {
		return nil, s.databaseStatus("record egress", err)
	}
	if err := d.Touch(ctx, req.GetDatabaseId()); err != nil {
		return nil, s.databaseStatus("touch", err)
	}
	return &scheddpb.TouchDatabaseResponse{}, nil
}

// ResizeDatabase changes a database's shape for the managed-Postgres
// driver; schedd restarts a running database on it.
func (s *Server) ResizeDatabase(ctx context.Context, req *scheddpb.ResizeDatabaseRequest) (*scheddpb.DatabaseRuntime, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	vm, err := d.Resize(ctx, req.GetDatabaseId(), dbsched.Shape{
		VcpuCount: int(req.GetVcpuCount()), MemMiB: int(req.GetMemMib()), VolumeBytes: req.GetVolumeBytes(),
	})
	if err != nil {
		return nil, s.databaseStatus("resize", err)
	}
	return databaseRuntime(vm), nil
}

// DatabaseUsage reports metered consumption for the managed-Postgres
// driver.
func (s *Server) DatabaseUsage(ctx context.Context, req *scheddpb.DatabaseUsageRequest) (*scheddpb.DatabaseUsageResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	if req.GetFrom().CheckValid() != nil || req.GetTo().CheckValid() != nil {
		return nil, status.Error(codes.InvalidArgument, "usage needs a window")
	}
	u, err := d.Usage(ctx, req.GetDatabaseId(), req.GetFrom().AsTime(), req.GetTo().AsTime())
	if err != nil {
		return nil, s.databaseStatus("usage", err)
	}
	return &scheddpb.DatabaseUsageResponse{
		ActiveSeconds: u.ActiveSeconds, ComputeUnitSeconds: u.ComputeUnitSeconds, StorageByteSeconds: u.StorageByteSeconds,
		HistoryByteSeconds: u.HistoryByteSeconds, EgressBytes: u.EgressBytes,
	}, nil
}

// DatabaseAdmin relays a superuser SQL batch. The SQL can carry role
// passwords and is never logged.
func (s *Server) DatabaseAdmin(ctx context.Context, req *scheddpb.DatabaseAdminRequest) (*scheddpb.DatabaseAdminResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	if err := (dbguest.Request{Op: dbguest.OpAdminSQL, SQL: req.GetSql()}).Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid admin sql")
	}
	resp, err := d.Admin(ctx, req.GetDatabaseId(), req.GetSql())
	if err != nil {
		return nil, s.databaseStatus("admin", err)
	}
	return &scheddpb.DatabaseAdminResponse{ErrorCode: resp.Error, Sqlstate: resp.SQLState}, nil
}

func (s *Server) DeleteDatabase(ctx context.Context, req *scheddpb.DatabaseRef) (*scheddpb.DeleteDatabaseResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	if err := d.Delete(ctx, req.GetDatabaseId()); err != nil {
		return nil, s.databaseStatus("delete", err)
	}
	return &scheddpb.DeleteDatabaseResponse{}, nil
}

func (s *Server) GetDatabase(ctx context.Context, req *scheddpb.DatabaseRef) (*scheddpb.DatabaseRuntime, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	vm, err := d.Get(ctx, req.GetDatabaseId())
	if err != nil {
		return nil, s.databaseStatus("get", err)
	}
	return databaseRuntime(vm), nil
}

func (s *Server) SetDatabaseAlwaysOn(ctx context.Context, req *scheddpb.SetDatabaseAlwaysOnRequest) (*scheddpb.DatabaseRuntime, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	vm, err := d.SetAlwaysOn(ctx, req.GetDatabaseId(), req.GetAlwaysOn())
	if err != nil {
		return nil, s.databaseStatus("set always-on", err)
	}
	return databaseRuntime(vm), nil
}

// databaseStatus maps scheduler errors to stable codes and logs the
// unexpected ones; internal detail never reaches the caller.
func (s *Server) databaseStatus(op string, err error) error {
	var code codes.Code
	switch {
	case errors.Is(err, dbsched.ErrNotFound):
		code = codes.NotFound
	case errors.Is(err, dbsched.ErrNoCapacity), errors.Is(err, dbsched.ErrBootSlotsBusy):
		code = codes.ResourceExhausted
	case errors.Is(err, dbsched.ErrConflict):
		code = codes.FailedPrecondition
	case errors.Is(err, dbsched.ErrNodeUnavailable):
		code = codes.Unavailable
	case errors.Is(err, dbsched.ErrInvalid):
		code = codes.InvalidArgument
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		code = codes.DeadlineExceeded
	default:
		if s.log != nil {
			s.log.Error("schedd: database rpc failed", "op", op, "err", err)
		}
		return status.Errorf(codes.Internal, "database %s failed", op)
	}
	return status.Errorf(code, "database %s: %v", op, err)
}
