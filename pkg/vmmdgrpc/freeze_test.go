// adr: 1151 — vmmd's freeze and thaw RPCs map refusals to stable codes, report
// the frozen state in Stats, and fail a forward to a frozen guest fast.
package vmmdgrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type freezeStubVMM struct {
	stubVMM
	freezeErr error
	thawErr   error
	frozen    bool
	since     time.Time
	awaitErr  error
	required  bool
	freezes   []fcvm.FreezeOptions
	thaws     []string
	thawOpts  []fcvm.ThawOptions
	resumed   []string
	// onAwait runs inside AwaitFrozen.
	onAwait func()
}

func (f *freezeStubVMM) Freeze(_ context.Context, _ string, opts fcvm.FreezeOptions) (fcvm.FreezeReport, error) {
	f.freezes = append(f.freezes, opts)
	return fcvm.FreezeReport{Pause: 400 * time.Microsecond, Reclaim: 9 * time.Millisecond, BeforeBytes: 200 << 20, AfterBytes: 60 << 20}, f.freezeErr
}

func (f *freezeStubVMM) ThawWith(_ context.Context, instance string, opts fcvm.ThawOptions) error {
	f.thaws = append(f.thaws, instance)
	f.thawOpts = append(f.thawOpts, opts)
	return f.thawErr
}

func (f *freezeStubVMM) AwaitFrozen(context.Context, string) (bool, error) {
	if f.onAwait != nil {
		f.onAwait()
	}
	return f.frozen, f.awaitErr
}

func (f *freezeStubVMM) FrozenState(string) (bool, time.Time) { return f.frozen, f.since }

func (f *freezeStubVMM) ImageHealthcheckRequiredFor(string) bool { return f.required }

func (f *freezeStubVMM) QuietResume(_ context.Context, instance string) error {
	f.resumed = append(f.resumed, instance)
	return nil
}

func (f *freezeStubVMM) NetnsFor(string) (string, bool) { return "fc-vm-a", true }

func (f *freezeStubVMM) InstancePID(string) (int, bool) { return 4242, true }

func problemCode(t *testing.T, err error) string {
	t.Helper()
	p, ok := grpcerr.FromStatus(err)
	if !ok || p == nil {
		t.Fatalf("error %v carries no problem", err)
	}
	return p.Code
}

func TestFreezeInstanceHandler(t *testing.T) {
	for _, tc := range []struct {
		name      string
		instance  string
		vmm       VmmdAPI
		wantCode  codes.Code
		wantProbe string
	}{
		{name: "frozen", instance: "vm-a", vmm: &freezeStubVMM{}, wantCode: codes.OK},
		{name: "missing instance", vmm: &freezeStubVMM{}, wantCode: codes.InvalidArgument, wantProbe: api.CodeValidation},
		{name: "no capability", instance: "vm-a", vmm: stubVMM{}, wantCode: codes.Unimplemented, wantProbe: api.CodeNotImplemented},
		{name: "not freezable", instance: "vm-a", vmm: &freezeStubVMM{freezeErr: fmt.Errorf("freeze vm-a: %w: job instance", fcvm.ErrNotFreezable)},
			wantCode: codes.FailedPrecondition, wantProbe: api.CodeInstanceNotFreezable},
		{name: "busy", instance: "vm-a", vmm: &freezeStubVMM{freezeErr: fmt.Errorf("freeze vm-a: %w", fcvm.ErrInstanceBusy)},
			wantCode: codes.FailedPrecondition, wantProbe: api.CodeInstanceBusy},
		{name: "pause failed", instance: "vm-a", vmm: &freezeStubVMM{freezeErr: errors.New("vmm: pause: boom")}, wantCode: codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.vmm, nil, "1.17.0", nil)
			resp, err := s.FreezeInstance(t.Context(), &vmmdpb.FreezeInstanceRequest{Instance: tc.instance, Reclaim: true})
			if status.Code(err) != tc.wantCode {
				t.Fatalf("FreezeInstance err = %v, want %v", err, tc.wantCode)
			}
			if tc.wantProbe != "" && problemCode(t, err) != tc.wantProbe {
				t.Fatalf("problem code = %q, want %q", problemCode(t, err), tc.wantProbe)
			}
			if tc.wantCode != codes.OK {
				return
			}
			want := &vmmdpb.FreezeInstanceResponse{Instance: "vm-a", PauseMicros: 400, ReclaimMicros: 9000,
				ResidentBytesBefore: 200 << 20, ResidentBytesAfter: 60 << 20}
			if resp.GetInstance() != want.GetInstance() || resp.GetPauseMicros() != want.GetPauseMicros() ||
				resp.GetReclaimMicros() != want.GetReclaimMicros() || resp.GetResidentBytesBefore() != want.GetResidentBytesBefore() ||
				resp.GetResidentBytesAfter() != want.GetResidentBytesAfter() {
				t.Fatalf("response = %v, want %v", resp, want)
			}
			if f := tc.vmm.(*freezeStubVMM); len(f.freezes) != 1 || !f.freezes[0].Reclaim {
				t.Fatalf("Freeze calls = %+v, want one with reclaim", f.freezes)
			}
		})
	}
}

func TestThawInstanceHandler(t *testing.T) {
	for _, tc := range []struct {
		name         string
		vmm          VmmdAPI
		required     bool
		afterRestore bool
		wantCode     codes.Code
		wantThaws    int
	}{
		{name: "thawed", vmm: &freezeStubVMM{}, wantCode: codes.OK, wantThaws: 1},
		{name: "thawed with image healthcheck", vmm: &freezeStubVMM{required: true}, required: true, wantCode: codes.OK, wantThaws: 1},
		{name: "thawed running after_restore", vmm: &freezeStubVMM{}, afterRestore: true, wantCode: codes.OK, wantThaws: 1},
		{name: "image healthcheck contract missing", vmm: &freezeStubVMM{}, required: true, wantCode: codes.InvalidArgument},
		{name: "no capability", vmm: stubVMM{}, wantCode: codes.Unimplemented},
		{name: "not live", vmm: &freezeStubVMM{thawErr: errors.New("thaw vm-a: instance is not live")}, wantCode: codes.Internal, wantThaws: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.vmm, nil, "1.17.0", nil)
			resp, err := s.ThawInstance(t.Context(), &vmmdpb.ThawInstanceRequest{Instance: "vm-a", ImageHealthcheckRequired: tc.required, RunAfterRestore: tc.afterRestore})
			if status.Code(err) != tc.wantCode {
				t.Fatalf("ThawInstance err = %v, want %v", err, tc.wantCode)
			}
			if f, ok := tc.vmm.(*freezeStubVMM); ok && len(f.thaws) != tc.wantThaws {
				t.Fatalf("Thaw calls = %v, want %d", f.thaws, tc.wantThaws)
			}
			if f, ok := tc.vmm.(*freezeStubVMM); ok && tc.wantThaws > 0 && f.thawOpts[0].RunResumeHook != tc.afterRestore {
				t.Fatalf("thaw options = %+v, want RunResumeHook=%v", f.thawOpts[0], tc.afterRestore)
			}
			if err == nil && (resp.GetInstance() != "vm-a" || resp.GetImageHealthcheckVerified() != tc.required || resp.GetAfterRestoreRan() != tc.afterRestore) {
				t.Fatalf("response = %v", resp)
			}
		})
	}
	if _, err := New(&freezeStubVMM{}, nil, "1.17.0", nil).ThawInstance(t.Context(), &vmmdpb.ThawInstanceRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ThawInstance without an instance = %v", err)
	}
}

func TestFrozenRefusalsMapToFailedPrecondition(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{err: fmt.Errorf("park vm-a: %w", fcvm.ErrInstanceFrozen), code: api.CodeFrozenInstanceRefused},
		{err: fmt.Errorf("freeze vm-a: %w: builder VM", fcvm.ErrNotFreezable), code: api.CodeInstanceNotFreezable},
		{err: fmt.Errorf("freeze vm-a: %w", fcvm.ErrInstanceBusy), code: api.CodeInstanceBusy},
		{err: fmt.Errorf("freeze vm-a: %w", fcvm.ErrCompressUnavailable), code: api.CodeFrozenCompressUnavailable},
	} {
		err := grpcerr.ToStatus(toProblem(tc.err))
		if status.Code(err) != codes.FailedPrecondition || problemCode(t, err) != tc.code {
			t.Fatalf("toProblem(%v) = %v, want FailedPrecondition %s", tc.err, err, tc.code)
		}
	}
}

func TestPrepareGuestFailsFastOnFrozenInstance(t *testing.T) {
	for _, tc := range []struct {
		name        string
		vmm         *freezeStubVMM
		wantCode    codes.Code
		wantProblem string
		wantResume  bool
	}{
		{name: "running", vmm: &freezeStubVMM{}, wantCode: codes.OK, wantResume: true},
		{name: "frozen", vmm: &freezeStubVMM{frozen: true}, wantCode: codes.Unavailable, wantProblem: api.CodeInstanceFrozen},
		{name: "caller gave up", vmm: &freezeStubVMM{awaitErr: context.Canceled}, wantCode: codes.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.vmm, nil, "1.17.0", nil)
			err := s.prepareGuest(t.Context(), "vm-a")
			if status.Code(err) != tc.wantCode {
				t.Fatalf("prepareGuest err = %v, want %v", err, tc.wantCode)
			}
			if tc.wantProblem != "" && problemCode(t, err) != tc.wantProblem {
				t.Fatalf("problem code = %q, want %q", problemCode(t, err), tc.wantProblem)
			}
			if resumed := len(tc.vmm.resumed) == 1; resumed != tc.wantResume {
				t.Fatalf("quiet resumes = %v, want resumed=%v", tc.vmm.resumed, tc.wantResume)
			}
		})
	}
	if err := New(stubVMM{}, nil, "1.17.0", nil).prepareGuest(t.Context(), "vm-a"); err != nil {
		t.Fatalf("prepareGuest on a VMM without the frozen tier: %v", err)
	}
}

type frozenUDPStream struct {
	grpc.ServerStream
	ctx  context.Context
	init *vmmdpb.ForwardUDPRequest
	sent int
}

func (s *frozenUDPStream) Context() context.Context { return s.ctx }

func (s *frozenUDPStream) Recv() (*vmmdpb.ForwardUDPRequest, error) {
	if s.init == nil {
		return nil, io.EOF
	}
	frame := s.init
	s.init = nil
	return frame, nil
}

func (s *frozenUDPStream) Send(*vmmdpb.ForwardUDPResponse) error {
	s.sent++
	return nil
}

// A forward marks its activity before it checks for a freeze, so a freeze
// deciding at that moment sees it; a frozen guest fails the forward before
// any bridge dials it.
func TestForwardToFrozenInstanceFailsFast(t *testing.T) {
	tracker := activity.NewWithDefaults()
	vmm := &freezeStubVMM{frozen: true}
	var inflightAtCheck int64
	vmm.onAwait = func() { inflightAtCheck, _ = tracker.Inflight("vm-a") }
	s := NewWithCPUAndNetAndActivity(vmm, nil, "1.17.0", nil, nil, nil, tracker)
	stream := &frozenUDPStream{ctx: t.Context(), init: &vmmdpb.ForwardUDPRequest{
		Frame: &vmmdpb.ForwardUDPRequest_Init{Init: &vmmdpb.ForwardUDPRequestInit{Instance: "vm-a", Port: 5353}},
	}}
	err := s.ForwardUDPStream(stream)
	if status.Code(err) != codes.Unavailable || problemCode(t, err) != api.CodeInstanceFrozen {
		t.Fatalf("ForwardUDPStream to a frozen instance = %v, want Unavailable %s", err, api.CodeInstanceFrozen)
	}
	if inflightAtCheck != 1 {
		t.Fatalf("in-flight activity during the frozen check = %d, want 1", inflightAtCheck)
	}
	if inflight, _ := tracker.Inflight("vm-a"); inflight != 0 || stream.sent != 0 || len(vmm.resumed) != 0 {
		t.Fatalf("after the refused forward: inflight=%d frames sent=%d quiet resumes=%v", inflight, stream.sent, vmm.resumed)
	}
}

func TestAddFrozenState(t *testing.T) {
	since := time.Unix(1_700_000_000, 0)
	for _, tc := range []struct {
		name       string
		vmm        VmmdAPI
		wantFrozen bool
	}{
		{name: "frozen", vmm: &freezeStubVMM{frozen: true, since: since}, wantFrozen: true},
		{name: "running", vmm: &freezeStubVMM{}},
		{name: "no capability", vmm: stubVMM{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.vmm, nil, "1.17.0", nil)
			row := &vmmdpb.InstanceStats{Instance: "vm-a"}
			s.addFrozenState(row)
			if row.GetFrozen() != tc.wantFrozen || (row.GetFrozenSince() != nil) != tc.wantFrozen {
				t.Fatalf("row = frozen %v since %v", row.GetFrozen(), row.GetFrozenSince())
			}
			if tc.wantFrozen && !row.GetFrozenSince().AsTime().Equal(since) {
				t.Fatalf("frozen_since = %v, want %v", row.GetFrozenSince().AsTime(), since)
			}
		})
	}
}
