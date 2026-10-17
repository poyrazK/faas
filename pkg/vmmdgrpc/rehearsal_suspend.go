package vmmdgrpc

import (
	"context"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type rehearsalSuspender interface {
	RehearseSuspendResume(context.Context, string, string) error
}

func (s *Server) RehearseSuspendResume(ctx context.Context, req *vmmdpb.RehearseSuspendResumeRequest) (*vmmdpb.RehearseSuspendResumeResponse, error) {
	for _, id := range []string{req.GetInstance(), req.GetReceiptId()} {
		u, err := uuid.Parse(id)
		if err != nil || u == uuid.Nil {
			return nil, status.Error(codes.InvalidArgument, "instance and receipt_id must be nonzero UUIDs")
		}
	}
	vmm, ok := s.vmm.(rehearsalSuspender)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "original-owner suspend/resume unavailable")
	}
	held := false
	if owner, ok := s.vmm.(interface{ RehearsalCheckpointHeld(string, string) bool }); ok {
		held = owner.RehearsalCheckpointHeld(req.GetInstance(), req.GetReceiptId())
	}
	started := time.Now()
	err := vmm.RehearseSuspendResume(ctx, req.GetInstance(), req.GetReceiptId())
	s.ops.Observe("RehearseSuspendResume", time.Since(started), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if !held {
		s.forgetForwarding(ctx, req.GetInstance())
	}
	return &vmmdpb.RehearseSuspendResumeResponse{Instance: req.GetInstance(), ReceiptId: req.GetReceiptId()}, nil
}
