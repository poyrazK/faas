// adr: 1027
package scheddgrpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbsched"
)

const testDBID = "0f8fad5b-d9cb-469f-a165-70867728950e"

type fakeDatabases struct {
	err       error
	vm        dbsched.VM
	admin     dbguest.Response
	adminSQL  string
	provision dbsched.ProvisionRequest
	restore   dbsched.RestoreRequest
	// usage answers Usage; egress sums RecordEgress.
	usage       dbsched.Usage
	usageWindow [2]time.Time
	egress      int64
	egressErr   error
	touchErr    error
	shape       dbsched.Shape
	importSpec  dbsched.ImportSpec
	importState dbguest.ImportStatus
}

func (f *fakeDatabases) Import(_ context.Context, _ string, spec dbsched.ImportSpec) (dbguest.ImportStatus, error) {
	f.importSpec = spec
	return f.importState, f.err
}
func (f *fakeDatabases) ImportStatus(context.Context, string) (dbguest.ImportStatus, error) {
	return f.importState, f.err
}

func (f *fakeDatabases) Provision(_ context.Context, req dbsched.ProvisionRequest) (dbsched.VM, error) {
	f.provision = req
	return f.vm, f.err
}
func (f *fakeDatabases) Wake(context.Context, string) (dbsched.VM, error) { return f.vm, f.err }
func (f *fakeDatabases) Park(context.Context, string) error               { return f.err }
func (f *fakeDatabases) Touch(context.Context, string) error {
	if f.touchErr != nil {
		return f.touchErr
	}
	return f.err
}
func (f *fakeDatabases) Admin(_ context.Context, _, sql string) (dbguest.Response, error) {
	f.adminSQL = sql
	return f.admin, f.err
}
func (f *fakeDatabases) Delete(context.Context, string) error            { return f.err }
func (f *fakeDatabases) Get(context.Context, string) (dbsched.VM, error) { return f.vm, f.err }
func (f *fakeDatabases) Resize(_ context.Context, _ string, shape dbsched.Shape) (dbsched.VM, error) {
	f.shape = shape
	vm := f.vm
	vm.VcpuCount, vm.MemMiB, vm.VolumeBytes = shape.VcpuCount, shape.MemMiB, shape.VolumeBytes
	return vm, f.err
}
func (f *fakeDatabases) Usage(_ context.Context, _ string, from, to time.Time) (dbsched.Usage, error) {
	f.usageWindow = [2]time.Time{from, to}
	return f.usage, f.err
}
func (f *fakeDatabases) RecordEgress(_ context.Context, _ string, bytes int64) error {
	f.egress += bytes
	return f.egressErr
}
func (f *fakeDatabases) Restore(_ context.Context, req dbsched.RestoreRequest) (dbsched.VM, error) {
	f.restore = req
	return f.vm, f.err
}
func (f *fakeDatabases) SetAlwaysOn(_ context.Context, _ string, alwaysOn bool) (dbsched.VM, error) {
	vm := f.vm
	vm.AlwaysOn = alwaysOn
	return vm, f.err
}

// asCaller attaches an mTLS peer whose certificate names cn; an empty cn
// models a unix-socket caller without TLS.
func asCaller(cn string) context.Context {
	p := &peer.Peer{}
	if cn != "" {
		p.AuthInfo = credentials.TLSInfo{State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: cn}}}}}
	}
	return peer.NewContext(context.Background(), p)
}

func TestDatabaseRPCsUnimplementedWithoutScheduler(t *testing.T) {
	s := New(nil, nil, nil)
	if _, err := s.WakeDatabase(asCaller(""), &scheddpb.DatabaseRef{DatabaseId: testDBID}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("WakeDatabase = %v, want Unimplemented", err)
	}
}

func TestDatabaseRPCCallerIdentity(t *testing.T) {
	s := New(nil, nil, nil).WithDatabases(&fakeDatabases{vm: dbsched.VM{DatabaseID: testDBID, State: dbsched.StateRunning}})
	ref := &scheddpb.DatabaseRef{DatabaseId: testDBID}
	cases := []struct {
		name   string
		caller string
		call   func(context.Context) error
		want   codes.Code
	}{
		{"apid wakes", callerAPID, func(c context.Context) error { _, err := s.WakeDatabase(c, ref); return err }, codes.OK},
		{"pgproxyd wakes", callerPgproxy, func(c context.Context) error { _, err := s.WakeDatabase(c, ref); return err }, codes.OK},
		{"pgproxyd touches", callerPgproxy, func(c context.Context) error {
			_, err := s.TouchDatabase(c, &scheddpb.TouchDatabaseRequest{DatabaseId: testDBID})
			return err
		}, codes.OK},
		{"pgproxyd cannot resize", callerPgproxy, func(c context.Context) error {
			_, err := s.ResizeDatabase(c, &scheddpb.ResizeDatabaseRequest{DatabaseId: testDBID})
			return err
		}, codes.PermissionDenied},
		{"pgproxyd cannot read usage", callerPgproxy, func(c context.Context) error {
			_, err := s.DatabaseUsage(c, &scheddpb.DatabaseUsageRequest{DatabaseId: testDBID})
			return err
		}, codes.PermissionDenied},
		{"gateway cannot wake", "gatewayd-internal-public.faas", func(c context.Context) error { _, err := s.WakeDatabase(c, ref); return err }, codes.PermissionDenied},
		{"pgproxyd cannot run admin sql", callerPgproxy, func(c context.Context) error {
			_, err := s.DatabaseAdmin(c, &scheddpb.DatabaseAdminRequest{DatabaseId: testDBID, Sql: "select 1"})
			return err
		}, codes.PermissionDenied},
		{"pgproxyd cannot delete", callerPgproxy, func(c context.Context) error { _, err := s.DeleteDatabase(c, ref); return err }, codes.PermissionDenied},
		{"pgproxyd cannot change always-on", callerPgproxy, func(c context.Context) error {
			_, err := s.SetDatabaseAlwaysOn(c, &scheddpb.SetDatabaseAlwaysOnRequest{DatabaseId: ref.GetDatabaseId(), AlwaysOn: true})
			return err
		}, codes.PermissionDenied},
		{"apid changes always-on", callerAPID, func(c context.Context) error {
			rt, err := s.SetDatabaseAlwaysOn(c, &scheddpb.SetDatabaseAlwaysOnRequest{DatabaseId: ref.GetDatabaseId(), AlwaysOn: true})
			if err == nil && !rt.GetAlwaysOn() {
				return status.Error(codes.Internal, "always_on not reported")
			}
			return err
		}, codes.OK},
		{"vmmd cannot provision", "vmmd.faas", func(c context.Context) error {
			_, err := s.ProvisionDatabase(c, &scheddpb.ProvisionDatabaseRequest{DatabaseId: testDBID, PostgresMajor: 17, VcpuCount: 1, MemMib: 384, VolumeBytes: 1 << 30})
			return err
		}, codes.PermissionDenied},
		{"pgproxyd cannot restore", callerPgproxy, func(c context.Context) error {
			_, err := s.RestoreDatabase(c, &scheddpb.RestoreDatabaseRequest{})
			return err
		}, codes.PermissionDenied},
		{"unix socket caller", "", func(c context.Context) error { _, err := s.GetDatabase(c, ref); return err }, codes.OK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := status.Code(tc.call(asCaller(tc.caller))); got != tc.want {
				t.Fatalf("code = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProvisionDatabaseValidatesAndMaps(t *testing.T) {
	f := &fakeDatabases{vm: dbsched.VM{DatabaseID: testDBID, NodeID: "db-a", State: dbsched.StateParked, Bootstrapped: true}}
	s := New(nil, nil, nil).WithDatabases(f)
	ctx := asCaller(callerAPID)
	if _, err := s.ProvisionDatabase(ctx, &scheddpb.ProvisionDatabaseRequest{DatabaseId: "nope", PostgresMajor: 17, VcpuCount: 1, MemMib: 384, VolumeBytes: 1 << 30}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad id = %v", err)
	}
	if _, err := s.ProvisionDatabase(ctx, &scheddpb.ProvisionDatabaseRequest{DatabaseId: testDBID, PostgresMajor: 12, VcpuCount: 1, MemMib: 384, VolumeBytes: 1 << 30}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad major = %v", err)
	}
	rt, err := s.ProvisionDatabase(ctx, &scheddpb.ProvisionDatabaseRequest{DatabaseId: testDBID, PostgresMajor: 17, VcpuCount: 2, MemMib: 512, VolumeBytes: 10 << 30})
	if err != nil || rt.GetNodeId() != "db-a" || rt.GetState() != "parked" || !rt.GetBootstrapped() {
		t.Fatalf("provision = %v, %v", rt, err)
	}
	if f.provision.VcpuCount != 2 || f.provision.MemMiB != 512 || f.provision.VolumeBytes != 10<<30 {
		t.Fatalf("request not relayed: %+v", f.provision)
	}
}

func TestDatabaseErrorsMapToStableCodes(t *testing.T) {
	cases := map[error]codes.Code{
		dbsched.ErrNotFound:                   codes.NotFound,
		dbsched.ErrNoCapacity:                 codes.ResourceExhausted,
		dbsched.ErrBootSlotsBusy:              codes.ResourceExhausted,
		dbsched.ErrConflict:                   codes.FailedPrecondition,
		dbsched.ErrNodeUnavailable:            codes.Unavailable,
		dbsched.ErrInvalid:                    codes.InvalidArgument,
		errors.New("dial tcp 10.0.0.9: boom"): codes.Internal,
	}
	for in, want := range cases {
		s := New(nil, nil, nil).WithDatabases(&fakeDatabases{err: in})
		_, err := s.WakeDatabase(asCaller(callerAPID), &scheddpb.DatabaseRef{DatabaseId: testDBID})
		if status.Code(err) != want {
			t.Errorf("%v -> %v, want %v", in, status.Code(err), want)
		}
		if want == codes.Internal && status.Convert(err).Message() != "database wake failed" {
			t.Errorf("internal detail leaked: %v", err)
		}
	}
}

func TestDatabaseAdminRelaysSQLState(t *testing.T) {
	f := &fakeDatabases{admin: dbguest.Response{Error: dbguest.ErrCodeAdminSQLFailed, SQLState: "42710"}}
	s := New(nil, nil, nil).WithDatabases(f)
	if _, err := s.DatabaseAdmin(asCaller(callerAPID), &scheddpb.DatabaseAdminRequest{DatabaseId: testDBID}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty sql = %v", err)
	}
	resp, err := s.DatabaseAdmin(asCaller(callerAPID), &scheddpb.DatabaseAdminRequest{DatabaseId: testDBID, Sql: "CREATE ROLE app"})
	if err != nil || resp.GetSqlstate() != "42710" || resp.GetErrorCode() != dbguest.ErrCodeAdminSQLFailed || f.adminSQL != "CREATE ROLE app" {
		t.Fatalf("admin = %v, %v (sql %q)", resp, err, f.adminSQL)
	}
}

func TestRestoreDatabaseValidatesAndMapsLineage(t *testing.T) {
	const sourceID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	target := time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC)
	f := &fakeDatabases{vm: dbsched.VM{DatabaseID: testDBID, NodeID: "db-a", State: dbsched.StateProvisioning,
		RestoreSource: sourceID, RestoreTarget: target, RestoreWindowSeconds: 3600, RestoreRecoveredTo: target.Add(-time.Second)}}
	s := New(nil, nil, nil).WithDatabases(f)
	ctx := asCaller(callerAPID)
	database := &scheddpb.ProvisionDatabaseRequest{DatabaseId: testDBID, PostgresMajor: 17, VcpuCount: 1, MemMib: 512, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600}
	for name, req := range map[string]*scheddpb.RestoreDatabaseRequest{
		"no database":     {SourceDatabaseId: sourceID, PointInTime: timestamppb.New(target)},
		"no point":        {Database: database, SourceDatabaseId: sourceID},
		"bad source":      {Database: database, SourceDatabaseId: "nope", PointInTime: timestamppb.New(target)},
		"itself":          {Database: database, SourceDatabaseId: testDBID, PointInTime: timestamppb.New(target)},
		"negative window": {Database: &scheddpb.ProvisionDatabaseRequest{DatabaseId: testDBID, PostgresMajor: 17, VcpuCount: 1, MemMib: 512, VolumeBytes: 1 << 30, RestoreWindowSeconds: -1}, SourceDatabaseId: sourceID, PointInTime: timestamppb.New(target)},
	} {
		if _, err := s.RestoreDatabase(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v, want InvalidArgument", name, err)
		}
	}
	rt, err := s.RestoreDatabase(ctx, &scheddpb.RestoreDatabaseRequest{Database: database, SourceDatabaseId: sourceID, PointInTime: timestamppb.New(target)})
	if err != nil || rt.GetState() != "provisioning" || rt.GetRestoreSourceId() != sourceID || !rt.GetRestoreTarget().AsTime().Equal(target) || rt.GetRestoreWindowSeconds() != 3600 ||
		!rt.GetRestoreRecoveredTo().AsTime().Equal(target.Add(-time.Second)) {
		t.Fatalf("restore = %v, %v", rt, err)
	}
	if f.restore.Source != sourceID || !f.restore.Target.Equal(target) || f.restore.MemMiB != 512 || f.restore.RestoreWindowSeconds != 3600 {
		t.Fatalf("request not relayed: %+v", f.restore)
	}
}

func TestTouchRecordsEgressBeforeActivity(t *testing.T) {
	f := &fakeDatabases{touchErr: dbsched.ErrConflict}
	s := New(nil, nil, nil).WithDatabases(f)
	_, err := s.TouchDatabase(asCaller(callerPgproxy), &scheddpb.TouchDatabaseRequest{DatabaseId: testDBID, EgressBytes: 4096})
	if status.Code(err) != codes.FailedPrecondition || f.egress != 4096 {
		t.Fatalf("touch of a parked database = %v, egress %d; the bytes must still land", err, f.egress)
	}
}

func TestDatabaseUsageMapsTheWindow(t *testing.T) {
	from := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	f := &fakeDatabases{usage: dbsched.Usage{ActiveSeconds: 1, ComputeUnitSeconds: 2, StorageByteSeconds: 3, HistoryByteSeconds: 4, EgressBytes: 5}}
	s := New(nil, nil, nil).WithDatabases(f)
	resp, err := s.DatabaseUsage(asCaller(callerAPID), &scheddpb.DatabaseUsageRequest{DatabaseId: testDBID, From: timestamppb.New(from), To: timestamppb.New(from.Add(time.Hour))})
	if err != nil || resp.GetActiveSeconds() != 1 || resp.GetComputeUnitSeconds() != 2 || resp.GetStorageByteSeconds() != 3 ||
		resp.GetHistoryByteSeconds() != 4 || resp.GetEgressBytes() != 5 || !f.usageWindow[0].Equal(from) {
		t.Fatalf("usage = %v, %v", resp, err)
	}
	if _, err := s.DatabaseUsage(asCaller(callerAPID), &scheddpb.DatabaseUsageRequest{DatabaseId: testDBID}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("usage without a window = %v", err)
	}
}

func TestResizeDatabaseRelaysTheShape(t *testing.T) {
	f := &fakeDatabases{vm: dbsched.VM{DatabaseID: testDBID, State: dbsched.StateRunning, ResizePending: true}}
	s := New(nil, nil, nil).WithDatabases(f)
	rt, err := s.ResizeDatabase(asCaller(callerAPID), &scheddpb.ResizeDatabaseRequest{DatabaseId: testDBID, VcpuCount: 2, MemMib: 1024, VolumeBytes: 4 << 30})
	if err != nil || f.shape != (dbsched.Shape{VcpuCount: 2, MemMiB: 1024, VolumeBytes: 4 << 30}) || rt.GetVcpuCount() != 2 || !rt.GetResizePending() {
		t.Fatalf("resize = %v, %v (shape %+v)", rt, err, f.shape)
	}
	f.err = dbsched.ErrNoCapacity
	if _, err := s.ResizeDatabase(asCaller(callerAPID), &scheddpb.ResizeDatabaseRequest{DatabaseId: testDBID, VcpuCount: 2, MemMib: 1024, VolumeBytes: 4 << 30}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("no capacity = %v", err)
	}
}

// The import RPC is apid's only, validates the source, and maps it; the
// password reaches the scheduler and nothing else.
func TestImportDatabaseValidatesAndMaps(t *testing.T) {
	started := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	f := &fakeDatabases{importState: dbguest.ImportStatus{State: dbguest.ImportRunning, StartedAt: started}}
	s := New(nil, nil, nil).WithDatabases(f)
	req := &scheddpb.ImportDatabaseRequest{DatabaseId: testDBID, Host: "ep-quiet-1.us-east-2.aws.neon.tech", Port: 5432, SourceDatabase: "gregale",
		User: "gregale_mig_ab", Password: "secret", TargetDatabase: "gregale", Owner: "gregale_schema_owner", SkipExtensions: []string{"neon"}}
	if _, err := s.ImportDatabase(asCaller(callerPgproxy), req); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("pgproxyd import = %v, want PermissionDenied", err)
	}
	got, err := s.ImportDatabase(asCaller(callerAPID), req)
	if err != nil || got.GetState() != "running" || !got.GetStartedAt().AsTime().Equal(started) {
		t.Fatalf("import = %+v, %v", got, err)
	}
	if spec := f.importSpec; spec.Host != req.Host || spec.Port != 5432 || spec.Source.Password != "secret" || spec.Source.Owner != "gregale_schema_owner" ||
		spec.Source.Database != "gregale" || len(spec.Source.SkipExtensions) != 1 {
		t.Fatalf("scheduler got %+v", spec)
	}
	bad := proto.Clone(req).(*scheddpb.ImportDatabaseRequest)
	bad.User = "Robert'); DROP"
	if _, err := s.ImportDatabase(asCaller(callerAPID), bad); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid user = %v, want InvalidArgument", err)
	}
	if got, err := s.DatabaseImportStatus(asCaller(callerAPID), &scheddpb.DatabaseRef{DatabaseId: testDBID}); err != nil || got.GetState() != "running" {
		t.Fatalf("status = %+v, %v", got, err)
	}
}
