// adr: 1027
package vmmdgrpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbvm"
)

const testDatabaseID = "0f8fad5b-d9cb-469f-a165-70867728950e"

type fakeDatabaseRuntime struct {
	calls     []string
	created   int64
	started   dbvm.BootRequest
	controls  []dbguest.Request
	stopped   string
	err       error
	dropErr   error
	controlRs dbguest.Response
}

func (f *fakeDatabaseRuntime) CreateVolume(_ context.Context, _ string, size int64) error {
	f.created = size
	return f.err
}
func (f *fakeDatabaseRuntime) DeleteVolume(context.Context, string) error {
	f.calls = append(f.calls, "delete_volume")
	return f.err
}
func (f *fakeDatabaseRuntime) DropArchive(context.Context, string) error {
	f.calls = append(f.calls, "drop_archive")
	return f.dropErr
}
func (f *fakeDatabaseRuntime) Start(_ context.Context, req dbvm.BootRequest) error {
	f.started = req
	return f.err
}
func (f *fakeDatabaseRuntime) Stop(_ context.Context, id string) error {
	f.stopped = id
	return f.err
}

// Dial returns one end of a pipe whose other end echoes, standing in for
// PostgreSQL behind the data port.
func (f *fakeDatabaseRuntime) Dial(context.Context, string) (net.Conn, error) {
	if f.err != nil {
		return nil, f.err
	}
	client, server := net.Pipe()
	go func() {
		defer func() { _ = server.Close() }()
		_, _ = io.Copy(server, server)
	}()
	return client, nil
}

// unixCaller models a caller on vmmd's unix socket (no TLS).
func unixCaller() context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{})
}

func tlsCaller(cn string) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: cn}}}},
	}})
}

func (f *fakeDatabaseRuntime) Control(_ context.Context, _ string, req dbguest.Request) (dbguest.Response, error) {
	f.controls = append(f.controls, req)
	return f.controlRs, f.err
}

func TestDatabaseRPCsUnimplementedWithoutRuntime(t *testing.T) {
	s := New(vmmStubBase{}, nil, "test", nil)
	ctx := unixCaller()
	calls := map[string]func() error{
		"create":  func() error { _, err := s.CreateDatabaseVolume(ctx, &vmmdpb.CreateDatabaseVolumeRequest{}); return err },
		"delete":  func() error { _, err := s.DeleteDatabaseVolume(ctx, &vmmdpb.DeleteDatabaseVolumeRequest{}); return err },
		"start":   func() error { _, err := s.StartDatabase(ctx, &vmmdpb.StartDatabaseRequest{}); return err },
		"stop":    func() error { _, err := s.StopDatabase(ctx, &vmmdpb.StopDatabaseRequest{}); return err },
		"control": func() error { _, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{}); return err },
	}
	for name, call := range calls {
		if code := status.Code(call()); code != codes.Unimplemented {
			t.Errorf("%s = %v, want Unimplemented", name, code)
		}
	}
}

func TestStartDatabaseValidatesShapeAndMapsRateLimits(t *testing.T) {
	rt := &fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateNeedsBootstrap}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	valid := &vmmdpb.StartDatabaseRequest{DatabaseId: testDatabaseID, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 384, VolumeBandwidthBytesPerSec: 50 << 20}
	resp, err := s.StartDatabase(unixCaller(), valid)
	if err != nil || resp.GetState() != string(dbguest.StateNeedsBootstrap) {
		t.Fatalf("StartDatabase = %v, %v", resp, err)
	}
	if rl := rt.started.VolumeRateLimiter; rl == nil || rl.Bandwidth == nil || rl.Bandwidth.Size != 50<<20 || rl.Bandwidth.RefillTimeMs != 1000 || rl.Ops != nil {
		t.Fatalf("rate limiter = %+v", rt.started.VolumeRateLimiter)
	}
	for name, mutate := range map[string]func(*vmmdpb.StartDatabaseRequest){
		"major":     func(r *vmmdpb.StartDatabaseRequest) { r.PostgresMajor = 12 },
		"vcpus":     func(r *vmmdpb.StartDatabaseRequest) { r.VcpuCount = 0 },
		"memory":    func(r *vmmdpb.StartDatabaseRequest) { r.MemSizeMib = 64 },
		"bandwidth": func(r *vmmdpb.StartDatabaseRequest) { r.VolumeBandwidthBytesPerSec = -1 },
		"restore without target": func(r *vmmdpb.StartDatabaseRequest) {
			r.RestoreSource = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
		},
		"restore without source": func(r *vmmdpb.StartDatabaseRequest) { r.RestoreTarget = timestamppb.Now() },
		"invalid target": func(r *vmmdpb.StartDatabaseRequest) {
			r.RestoreSource, r.RestoreTarget = "7c9e6679-7425-40de-944b-e07fc1f90ae7", &timestamppb.Timestamp{Nanos: -1}
		},
	} {
		req := &vmmdpb.StartDatabaseRequest{DatabaseId: testDatabaseID, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 384}
		mutate(req)
		if _, err := s.StartDatabase(unixCaller(), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: StartDatabase = %v, want InvalidArgument", name, err)
		}
	}
}

// Deleting a database drops its archive first, so an unreachable
// dbbackupd leaves the volume for schedd's retry to find.
func TestDeleteDatabaseVolumeDropsTheArchiveFirst(t *testing.T) {
	rt := &fakeDatabaseRuntime{}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	if _, err := s.DeleteDatabaseVolume(unixCaller(), &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: testDatabaseID}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rt.calls, ",") != "delete_volume" {
		t.Fatalf("volume-only delete = %v", rt.calls)
	}
	rt.calls = nil
	if _, err := s.DeleteDatabaseVolume(unixCaller(), &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: testDatabaseID, Archive: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rt.calls, ",") != "drop_archive,delete_volume" {
		t.Fatalf("database delete = %v", rt.calls)
	}
	rt.calls, rt.dropErr = nil, errors.New("dial unix: connection refused")
	if _, err := s.DeleteDatabaseVolume(unixCaller(), &vmmdpb.DeleteDatabaseVolumeRequest{DatabaseId: testDatabaseID, Archive: true}); status.Code(err) != codes.Internal {
		t.Fatalf("drop failure = %v", err)
	}
	if strings.Join(rt.calls, ",") != "drop_archive" {
		t.Fatalf("volume deleted although the archive was not: %v", rt.calls)
	}
}

func TestStartDatabaseRelaysARestore(t *testing.T) {
	rt := &fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateStarting}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	target := time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC)
	_, err := s.StartDatabase(unixCaller(), &vmmdpb.StartDatabaseRequest{DatabaseId: testDatabaseID, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 384,
		RestoreSource: "7c9e6679-7425-40de-944b-e07fc1f90ae7", RestoreTarget: timestamppb.New(target), RestoreWindowSeconds: 3600, RestoreUntilEnd: true})
	if err != nil || rt.started.RestoreSource != "7c9e6679-7425-40de-944b-e07fc1f90ae7" || !rt.started.RestoreTarget.Equal(target) || rt.started.RestoreWindowSeconds != 3600 || !rt.started.RestoreUntilEnd {
		t.Fatalf("StartDatabase = %v; boot %+v", err, rt.started)
	}
}

func TestDatabaseControlRelaysFlushInstant(t *testing.T) {
	recovered := time.Date(2026, 10, 13, 9, 29, 0, 0, time.UTC)
	rt := &fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateReady, RecoveredTo: recovered}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	if _, err := s.DatabaseControl(unixCaller(), &vmmdpb.DatabaseControlRequest{DatabaseId: testDatabaseID, Op: "flush_wal"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("flush without an instant = %v", err)
	}
	after := time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC)
	resp, err := s.DatabaseControl(unixCaller(), &vmmdpb.DatabaseControlRequest{DatabaseId: testDatabaseID, Op: "flush_wal", After: timestamppb.New(after)})
	if err != nil || !resp.GetRecoveredTo().AsTime().Equal(recovered) {
		t.Fatalf("control = %v, %v", resp, err)
	}
	if len(rt.controls) != 1 || rt.controls[0].Op != dbguest.OpFlushWAL || !rt.controls[0].After.Equal(after) {
		t.Fatalf("flush not relayed: %+v", rt.controls)
	}
}

func TestDatabaseControlValidatesAndRelays(t *testing.T) {
	rt := &fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateReady, Major: 17, Error: dbguest.ErrCodeAdminSQLFailed, SQLState: "42710"}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	ctx := unixCaller()
	for _, req := range []*vmmdpb.DatabaseControlRequest{
		{DatabaseId: testDatabaseID, Op: "stop"},
		{DatabaseId: testDatabaseID, Op: "drop"},
		{DatabaseId: testDatabaseID, Op: ""},
		{DatabaseId: testDatabaseID, Op: "admin_sql"},
		{DatabaseId: testDatabaseID, Op: "bootstrap", Sql: "select 1"},
	} {
		if _, err := s.DatabaseControl(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%+v = %v, want InvalidArgument", req, err)
		}
	}
	if len(rt.controls) != 0 {
		t.Fatalf("invalid requests reached the guest: %+v", rt.controls)
	}
	resp, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: testDatabaseID, Op: "admin_sql", Sql: "create role app login"})
	if err != nil || resp.GetErrorCode() != dbguest.ErrCodeAdminSQLFailed || resp.GetSqlstate() != "42710" {
		t.Fatalf("admin_sql = %v, %v", resp, err)
	}
	if len(rt.controls) != 1 || rt.controls[0].SQL != "create role app login" {
		t.Fatalf("admin_sql not relayed: %+v", rt.controls)
	}
}

func TestDatabaseStatusCodesHideInternalDetail(t *testing.T) {
	cases := map[error]codes.Code{
		dbvm.ErrInvalidID:                         codes.InvalidArgument,
		fmt.Errorf("x: %w", dbvm.ErrUnknownMajor): codes.InvalidArgument,
		dbvm.ErrVolumeExists:                      codes.AlreadyExists,
		dbvm.ErrVolumeNotFound:                    codes.NotFound,
		dbvm.ErrRunning:                           codes.FailedPrecondition,
		dbvm.ErrNotRunning:                        codes.FailedPrecondition,
		context.DeadlineExceeded:                  codes.DeadlineExceeded,
		errors.New("mount /srv/db/x: busy"):       codes.Internal,
	}
	s := New(vmmStubBase{}, nil, "test", nil)
	for in, want := range cases {
		err := s.databaseStatus("start", in)
		if status.Code(err) != want {
			t.Errorf("%v -> %v, want %v", in, status.Code(err), want)
		}
		if want == codes.Internal && strings.Contains(err.Error(), "/srv/db") {
			t.Errorf("internal detail leaked: %v", err)
		}
	}
}

func TestCreateDatabaseVolumeBounds(t *testing.T) {
	rt := &fakeDatabaseRuntime{}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	if _, err := s.CreateDatabaseVolume(unixCaller(), &vmmdpb.CreateDatabaseVolumeRequest{DatabaseId: testDatabaseID, SizeBytes: 1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("tiny volume = %v", err)
	}
	if _, err := s.CreateDatabaseVolume(unixCaller(), &vmmdpb.CreateDatabaseVolumeRequest{DatabaseId: testDatabaseID, SizeBytes: 10 << 30}); err != nil || rt.created != 10<<30 {
		t.Fatalf("create = %v, size %d", err, rt.created)
	}
}

func TestDatabaseRPCCallerIdentity(t *testing.T) {
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(&fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateReady}})
	start := func(ctx context.Context) error {
		_, err := s.StartDatabase(ctx, &vmmdpb.StartDatabaseRequest{DatabaseId: testDatabaseID, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 384})
		return err
	}
	admin := func(ctx context.Context) error {
		_, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: testDatabaseID, Op: "admin_sql", Sql: "select 1"})
		return err
	}
	cases := []struct {
		name string
		ctx  context.Context
		call func(context.Context) error
		want codes.Code
	}{
		{"schedd starts", tlsCaller("schedd.faas"), start, codes.OK},
		{"unix caller starts", unixCaller(), start, codes.OK},
		{"pgproxyd cannot start", tlsCaller("pgproxyd.faas"), start, codes.PermissionDenied},
		{"pgproxyd cannot run admin sql", tlsCaller("pgproxyd.faas"), admin, codes.PermissionDenied},
		{"gateway cannot run admin sql", tlsCaller("gatewayd-internal-public.faas"), admin, codes.PermissionDenied},
		{"no peer", context.Background(), start, codes.PermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := status.Code(tc.call(tc.ctx)); got != tc.want {
				t.Fatalf("code = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDatabaseSessionRelaysBytes runs a session over a real gRPC stream to
// an echoing data port.
func TestDatabaseSessionRelaysBytes(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	gsrv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(gsrv, New(vmmStubBase{}, nil, "test", nil).WithDatabases(&fakeDatabaseRuntime{}))
	go func() { _ = gsrv.Serve(lis) }()
	defer gsrv.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	stream, err := vmmdpb.NewVmmdClient(conn).DatabaseSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&vmmdpb.DatabaseSessionFrame{DatabaseId: testDatabaseID, Data: []byte("hello ")}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&vmmdpb.DatabaseSessionFrame{Data: []byte("postgres")}); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for len(got) < len("hello postgres") {
		frame, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv after %q: %v", got, err)
		}
		got = append(got, frame.GetData()...)
	}
	if string(got) != "hello postgres" {
		t.Fatalf("echo = %q", got)
	}
	_ = stream.CloseSend()
}
