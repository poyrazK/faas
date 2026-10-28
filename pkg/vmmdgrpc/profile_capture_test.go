// adr: 1155 — on-demand captures hold instance activity, resume a quiet-paused
// guest (ADR-1151) first, and map guest refusals to gRPC codes.
package vmmdgrpc_test

import (
	"context"
	"errors"
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

type quietCaptureVMM struct {
	captureVMM
	resumeErr error
	resumed   []string
}

func (v *quietCaptureVMM) QuietResume(_ context.Context, instance string) error {
	v.resumed = append(v.resumed, instance)
	return v.resumeErr
}

// TestCaptureProfileResumesQuietPausedGuest pins ADR-1155 with ADR-1151: a
// quiet-paused guest is resumed before the capture reaches it, and a failed
// resume fails the capture without contacting the guest.
func TestCaptureProfileResumesQuietPausedGuest(t *testing.T) {
	req := &vmmdpb.CaptureProfileRequest{Instance: "i-1", CaptureId: strings.Repeat("ab", 16), Kinds: []string{"cpu"}, DurationMs: 2000}
	for _, tc := range []struct {
		name        string
		resumeErr   error
		wantCode    codes.Code
		wantCapture bool
	}{
		{"resumed", nil, codes.OK, true},
		{"resume failed", errors.New("firecracker resume failed"), codes.Unavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := &quietCaptureVMM{resumeErr: tc.resumeErr}
			captured := false
			vmm.fn = func(_ context.Context, instance string, _ profileproto.CaptureRequest) (profileproto.CaptureResult, error) {
				if len(vmm.resumed) != 1 || vmm.resumed[0] != instance {
					t.Errorf("guest contacted before resume: resumed %v", vmm.resumed)
				}
				captured = true
				return profileproto.CaptureResult{Profiles: []profileproto.CapturedProfile{}}, nil
			}
			srv := vmmdgrpc.NewWithCPUAndNetAndActivity(vmm, wire.NewOpsMetrics("vmmd_test"), "1.0.0", nil, nil, nil, activity.New(nil))
			_, err := srv.CaptureProfile(t.Context(), req)
			if status.Code(err) != tc.wantCode || captured != tc.wantCapture {
				t.Fatalf("err = %v, captured = %v; want %v, %v", err, captured, tc.wantCode, tc.wantCapture)
			}
		})
	}
}

func TestCaptureProfileHoldsActivityAndMapsResult(t *testing.T) {
	tracker := activity.New(nil)
	vmm := &captureVMM{}
	srv := vmmdgrpc.NewWithCPUAndNetAndActivity(vmm, wire.NewOpsMetrics("vmmd_test"), "1.0.0", nil, nil, nil, tracker)
	vmm.fn = func(_ context.Context, instance string, req profileproto.CaptureRequest) (profileproto.CaptureResult, error) {
		if n, _ := tracker.Inflight(instance); n != 1 {
			t.Errorf("capture not counted as in-flight activity: %d", n)
		}
		if req.CheckpointMillis != 1000 {
			t.Errorf("checkpoint interval not forwarded: %d", req.CheckpointMillis)
		}
		return profileproto.CaptureResult{Profiles: []profileproto.CapturedProfile{{Kind: req.Kinds[0], ProcessID: "4", Profile: []byte("p"), FromUnixNano: 1, UntilUnixNano: 2}}, Processes: 1, Reason: "r", Partial: true, OOMKilled: true, OOMPeakMB: 240}, nil
	}
	req := &vmmdpb.CaptureProfileRequest{Instance: "i-1", CaptureId: strings.Repeat("ab", 16), Kinds: []string{"heap"}, DurationMs: 2000, CheckpointMs: 1000}
	resp, err := srv.CaptureProfile(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetProfiles()) != 1 || resp.GetProfiles()[0].GetKind() != "heap" || string(resp.GetProfiles()[0].GetProfile()) != "p" || resp.GetProcesses() != 1 || resp.GetReason() != "r" || !resp.GetPartial() || !resp.GetOomKilled() || resp.GetOomPeakMb() != 240 {
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
