package scheddgrpc

import (
	"context"
	"errors"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

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
	return &scheddpb.DatabaseRuntime{
		DatabaseId: vm.DatabaseID, NodeId: vm.NodeID, State: string(vm.State),
		Bootstrapped: vm.Bootstrapped, LastError: vm.LastError, VmmdTarget: vm.NodeTarget,
	}
}

func (s *Server) ProvisionDatabase(ctx context.Context, req *scheddpb.ProvisionDatabaseRequest) (*scheddpb.DatabaseRuntime, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	pr := dbsched.ProvisionRequest{
		DatabaseID: req.GetDatabaseId(), Major: int(req.GetPostgresMajor()), VcpuCount: int(req.GetVcpuCount()),
		MemMiB: int(req.GetMemMib()), VolumeBytes: req.GetVolumeBytes(),
	}
	if err := pr.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	vm, err := d.Provision(ctx, pr)
	if err != nil {
		return nil, s.databaseStatus("provision", err)
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

func (s *Server) TouchDatabase(ctx context.Context, req *scheddpb.DatabaseRef) (*scheddpb.TouchDatabaseResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID, callerPgproxy)
	if err != nil {
		return nil, err
	}
	if err := d.Touch(ctx, req.GetDatabaseId()); err != nil {
		return nil, s.databaseStatus("touch", err)
	}
	return &scheddpb.TouchDatabaseResponse{}, nil
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

// databaseStatus maps scheduler errors to stable codes and logs the
// unexpected ones; internal detail never reaches the caller.
func (s *Server) databaseStatus(op string, err error) error {
	var code codes.Code
	switch {
	case errors.Is(err, dbsched.ErrNotFound):
		code = codes.NotFound
	case errors.Is(err, dbsched.ErrNoCapacity):
		code = codes.ResourceExhausted
	case errors.Is(err, dbsched.ErrConflict):
		code = codes.FailedPrecondition
	case errors.Is(err, dbsched.ErrNodeUnavailable):
		code = codes.Unavailable
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
