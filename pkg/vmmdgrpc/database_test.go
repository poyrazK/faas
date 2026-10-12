// adr: 1027
package vmmdgrpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbvm"
)

const testDatabaseID = "0f8fad5b-d9cb-469f-a165-70867728950e"

type fakeDatabaseRuntime struct {
	created   int64
	started   dbvm.BootRequest
	controls  []dbguest.Request
	stopped   string
	err       error
	controlRs dbguest.Response
}

func (f *fakeDatabaseRuntime) CreateVolume(_ context.Context, _ string, size int64) error {
	f.created = size
	return f.err
}
func (f *fakeDatabaseRuntime) DeleteVolume(context.Context, string) error { return f.err }
func (f *fakeDatabaseRuntime) Start(_ context.Context, req dbvm.BootRequest) error {
	f.started = req
	return f.err
}
func (f *fakeDatabaseRuntime) Stop(_ context.Context, id string) error {
	f.stopped = id
	return f.err
}
func (f *fakeDatabaseRuntime) Control(_ context.Context, _ string, req dbguest.Request) (dbguest.Response, error) {
	f.controls = append(f.controls, req)
	return f.controlRs, f.err
}

func TestDatabaseRPCsUnimplementedWithoutRuntime(t *testing.T) {
	s := New(vmmStubBase{}, nil, "test", nil)
	ctx := context.Background()
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
	resp, err := s.StartDatabase(context.Background(), valid)
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
	} {
		req := &vmmdpb.StartDatabaseRequest{DatabaseId: testDatabaseID, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 384}
		mutate(req)
		if _, err := s.StartDatabase(context.Background(), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: StartDatabase = %v, want InvalidArgument", name, err)
		}
	}
}

func TestDatabaseControlAllowsOnlyStatusAndBootstrap(t *testing.T) {
	rt := &fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateReady, Major: 17}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	ctx := context.Background()
	for _, op := range []string{"stop", "drop", ""} {
		if _, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: testDatabaseID, Op: op}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("op %q = %v, want InvalidArgument", op, err)
		}
	}
	if _, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: testDatabaseID, Op: "bootstrap", SuperuserPassword: "short"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("weak bootstrap = %v, want InvalidArgument", err)
	}
	if len(rt.controls) != 0 {
		t.Fatalf("invalid requests reached the guest: %+v", rt.controls)
	}
	resp, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: testDatabaseID, Op: "bootstrap", SuperuserPassword: strings.Repeat("p", 40)})
	if err != nil || resp.GetState() != "ready" || resp.GetPostgresMajor() != 17 {
		t.Fatalf("bootstrap = %v, %v", resp, err)
	}
	if len(rt.controls) != 1 || rt.controls[0].SuperuserPassword != strings.Repeat("p", 40) {
		t.Fatalf("bootstrap not relayed: %+v", rt.controls)
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
	if _, err := s.CreateDatabaseVolume(context.Background(), &vmmdpb.CreateDatabaseVolumeRequest{DatabaseId: testDatabaseID, SizeBytes: 1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("tiny volume = %v", err)
	}
	if _, err := s.CreateDatabaseVolume(context.Background(), &vmmdpb.CreateDatabaseVolumeRequest{DatabaseId: testDatabaseID, SizeBytes: 10 << 30}); err != nil || rt.created != 10<<30 {
		t.Fatalf("create = %v, size %d", err, rt.created)
	}
}
