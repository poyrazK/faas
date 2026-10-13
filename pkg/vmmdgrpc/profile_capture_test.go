package vmmdgrpc_test

import (
	"context"
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type captureVMM struct {
	fakeVMM
	fn func(context.Context, string, profileproto.CaptureRequest) (profileproto.CaptureResult, error)
}

func (v *captureVMM) CaptureProfile(ctx context.Context, instance string, req profileproto.CaptureRequest) (profileproto.CaptureResult, error) {
	return v.fn(ctx, instance, req)
}

func TestCaptureProfileHoldsActivityAndMapsResult(t *testing.T) {
	tracker := activity.New(nil)
	vmm := &captureVMM{}
	srv := vmmdgrpc.NewWithCPUAndNetAndActivity(vmm, wire.NewOpsMetrics("vmmd_test"), "1.0.0", nil, nil, nil, tracker)
	vmm.fn = func(_ context.Context, instance string, req profileproto.CaptureRequest) (profileproto.CaptureResult, error) {
		if n, _ := tracker.Inflight(instance); n != 1 {
			t.Errorf("capture not counted as in-flight activity: %d", n)
		}
		return profileproto.CaptureResult{Profiles: []profileproto.CapturedProfile{{Kind: req.Kinds[0], ProcessID: "4", Profile: []byte("p"), FromUnixNano: 1, UntilUnixNano: 2}}, Processes: 1, Reason: "r"}, nil
	}
	req := &vmmdpb.CaptureProfileRequest{Instance: "i-1", CaptureId: strings.Repeat("ab", 16), Kinds: []string{"heap"}, DurationMs: 2000}
	resp, err := srv.CaptureProfile(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetProfiles()) != 1 || resp.GetProfiles()[0].GetKind() != "heap" || string(resp.GetProfiles()[0].GetProfile()) != "p" || resp.GetProcesses() != 1 || resp.GetReason() != "r" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if n, _ := tracker.Inflight("i-1"); n != 0 {
		t.Fatalf("activity leaked after capture: %d", n)
	}

	for _, tt := range []struct {
		err  error
		code codes.Code
	}{
		{fcvm.ErrProfileCaptureNotRunning, codes.NotFound},
		{fcvm.ErrProfileCaptureBusy, codes.FailedPrecondition},
		{fcvm.ErrProfileCaptureUnsupported, codes.FailedPrecondition},
		{context.DeadlineExceeded, codes.DeadlineExceeded},
	} {
		vmm.fn = func(context.Context, string, profileproto.CaptureRequest) (profileproto.CaptureResult, error) {
			return profileproto.CaptureResult{}, tt.err
		}
		if _, err := srv.CaptureProfile(t.Context(), req); status.Code(err) != tt.code {
			t.Fatalf("%v mapped to %v, want %v", tt.err, status.Code(err), tt.code)
		}
	}
	bad := &vmmdpb.CaptureProfileRequest{Instance: "i-1", CaptureId: "short", Kinds: []string{"cpu"}, DurationMs: 2000}
	if _, err := srv.CaptureProfile(t.Context(), bad); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid request: %v", err)
	}
}
