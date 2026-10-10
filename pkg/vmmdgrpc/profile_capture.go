package vmmdgrpc

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type profileCapturer interface {
	CaptureProfile(context.Context, string, profileproto.CaptureRequest) (profileproto.CaptureResult, error)
}

// CaptureProfile runs an on-demand guest profile capture (ADR-967). The
// window counts as in-flight activity so schedd's idle reaper keeps the
// instance RUNNING until the capture returns.
func (s *Server) CaptureProfile(ctx context.Context, req *vmmdpb.CaptureProfileRequest) (resp *vmmdpb.CaptureProfileResponse, err error) {
	const op = "CaptureProfile"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), err) }()
	capture := profileproto.CaptureRequest{CaptureID: req.GetCaptureId(), Kinds: req.GetKinds(), DurationMillis: req.GetDurationMs()}
	if req.GetInstance() == "" {
		return nil, status.Error(codes.InvalidArgument, "instance is required")
	}
	if verr := capture.Validate(); verr != nil {
		return nil, status.Error(codes.InvalidArgument, verr.Error())
	}
	capturer, ok := s.vmm.(profileCapturer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "this vmmd does not support profile captures")
	}
	s.beginActivity(req.GetInstance())
	defer s.endActivity(req.GetInstance())
	result, cerr := capturer.CaptureProfile(ctx, req.GetInstance(), capture)
	if cerr != nil {
		return nil, profileCaptureStatus(cerr)
	}
	return profileCaptureResponse(result), nil
}

func profileCaptureStatus(err error) error {
	switch {
	case errors.Is(err, fcvm.ErrProfileCaptureNotRunning):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, fcvm.ErrProfileCaptureUnsupported), errors.Is(err, fcvm.ErrProfileCaptureBusy), errors.Is(err, fcvm.ErrProfileCaptureSuspended):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return status.Error(codes.DeadlineExceeded, "profile capture timed out")
	default:
		return status.Error(codes.Unavailable, "profile capture failed")
	}
}

func profileCaptureResponse(result profileproto.CaptureResult) *vmmdpb.CaptureProfileResponse {
	out := &vmmdpb.CaptureProfileResponse{Processes: int32(result.Processes), Dropped: int32(result.Dropped), Reason: result.Reason}
	for _, p := range result.Profiles {
		out.Profiles = append(out.Profiles, &vmmdpb.CapturedProfile{Kind: p.Kind, ProcessId: p.ProcessID, Profile: p.Profile, FromUnixNano: p.FromUnixNano, UntilUnixNano: p.UntilUnixNano})
	}
	return out
}
