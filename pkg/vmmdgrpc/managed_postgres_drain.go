package vmmdgrpc

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/postgresdrainwire"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type managedPostgresDrainVMM interface {
	RetireManagedPostgresDrain(context.Context, state.ManagedPostgresDrainFrame) (state.ManagedPostgresDrainProof, error)
}

func (s *Server) WithManagedPostgresDrainAuthority(authority state.ManagedPostgresDrainAuthority) *Server {
	s.managedPostgresDrainAuthority = authority
	return s
}

func (s *Server) managedPostgresDrainFrame(ctx context.Context, req *vmmdpb.RetireManagedPostgresDrainRequest) (state.ManagedPostgresDrainFrame, error) {
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 {
		return state.ManagedPostgresDrainFrame{}, state.ErrInvalidArgument
	}
	f, err := postgresdrainwire.FrameFromProto(req.GetFrame())
	if err != nil {
		return f, err
	}
	if s.nodeID == "" || s.nodeID != f.NodeID {
		return f, state.ErrConflict
	}
	md, _ := metadata.FromIncomingContext(ctx)
	for key, want := range map[string]string{"x-faas-node-id": f.NodeID, "x-faas-instance-id": f.InstanceID,
		"x-faas-app-id": f.AppID, "x-faas-deployment-id": f.DeploymentID, "x-faas-wake-id": f.WakeID} {
		values := md.Get(key)
		if len(values) != 1 || values[0] != want {
			return f, state.ErrInvalidArgument
		}
	}
	return f, nil
}

func (s *Server) RetireManagedPostgresDrain(ctx context.Context, req *vmmdpb.RetireManagedPostgresDrainRequest) (response *vmmdpb.RetireManagedPostgresDrainResponse, result error) {
	start := time.Now()
	defer func() { s.ops.Observe("RetireManagedPostgresDrain", time.Since(start), result) }()
	vmm, ok := s.vmm.(managedPostgresDrainVMM)
	if !ok || s.managedPostgresDrainAuthority == nil {
		return nil, status.Error(codes.Unimplemented, "journal-backed cutover retirement unavailable")
	}
	frame, err := s.managedPostgresDrainFrame(ctx, req)
	if err != nil {
		return nil, managedPostgresDrainStatus(err)
	}
	proof, err := s.managedPostgresDrainAuthority.WithManagedPostgresDrainAuthority(ctx, frame, func(stopCtx context.Context) (state.ManagedPostgresDrainProof, error) {
		return vmm.RetireManagedPostgresDrain(stopCtx, frame)
	})
	if err != nil {
		return nil, managedPostgresDrainStatus(err)
	}
	encoded, err := postgresdrainwire.ProofToProto(proof, frame)
	if err != nil {
		return nil, managedPostgresDrainStatus(err)
	}
	return &vmmdpb.RetireManagedPostgresDrainResponse{Frame: req.Frame, Proof: encoded}, nil
}

// Journal paths and physical ownership details never cross diagnostic text.
func managedPostgresDrainStatus(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "cutover retirement canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "cutover retirement deadline elapsed")
	case errors.Is(err, state.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, "invalid cutover retirement frame")
	case errors.Is(err, state.ErrConflict):
		return status.Error(codes.FailedPrecondition, "cutover ownership or native retirement unconfirmed")
	default:
		return status.Error(codes.Unavailable, "cutover retirement could not be confirmed")
	}
}
