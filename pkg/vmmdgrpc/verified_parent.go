package vmmdgrpc

// adr: 431

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type verifiedParentMaterializer interface {
	MaterializeVerifiedParentExt4(context.Context, imagechain.ParentMaterialization) (imagechain.ParentMaterialization, error)
}

func (s *Server) MaterializeVerifiedParentExt4(ctx context.Context, req *vmmdpb.MaterializeVerifiedParentExt4Request) (*vmmdpb.MaterializeVerifiedParentExt4Response, error) {
	start := time.Now()
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 {
		return nil, status.Error(codes.InvalidArgument, "unsupported parent materialization request")
	}
	expected := imagechain.ParentMaterialization{Artifact: imagechain.BaseArtifact{StorageKey: req.StorageKey, Digest: req.ArtifactDigest, Bytes: req.ArtifactBytes}, TargetDir: req.TargetDir}
	if !expected.Valid() || !sched.IsParentBaseKey(expected.Artifact.StorageKey) {
		return nil, status.Error(codes.InvalidArgument, "invalid parent materialization identity")
	}
	owner, ok := s.vmm.(verifiedParentMaterializer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "verified parent materialization unavailable")
	}
	actual, err := owner.MaterializeVerifiedParentExt4(ctx, expected)
	s.ops.Observe("MaterializeVerifiedParentExt4", time.Since(start), err)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "verified parent materialization failed")
	}
	if actual != expected || ctx.Err() != nil {
		return nil, status.Error(codes.FailedPrecondition, "parent materialization receipt mismatch")
	}
	return &vmmdpb.MaterializeVerifiedParentExt4Response{StorageKey: actual.Artifact.StorageKey, TargetDir: actual.TargetDir, ArtifactDigest: actual.Artifact.Digest, ArtifactBytes: actual.Artifact.Bytes}, nil
}
