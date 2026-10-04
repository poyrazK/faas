package githubdgrpc

import (
	"context"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) GetProtectedBranchEvidence(ctx context.Context, req *githubdpb.GetProtectedBranchEvidenceRequest) (*githubdpb.GetProtectedBranchEvidenceResponse, error) {
	const op = "GetProtectedBranchEvidence"
	service, ok := s.svc.(ProtectedBranchEvidenceService)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "githubd: protected branch evidence is not configured")
	}
	start := time.Now()
	evidence, err := service.GetProtectedBranchEvidence(ctx, req.GetAccountId(), req.GetInstallationId(), req.GetRepositoryId(), req.GetRepoFullName(), req.GetBranch(), req.GetCommitSha())
	if err == nil && evidence.Qualified && !evidence.ValidFor(req.GetInstallationId(), req.GetRepositoryId(), req.GetRepoFullName(), req.GetBranch(), req.GetCommitSha(), time.Now().UTC(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) {
		err = api.ErrSourceRefUnavailable("Protected branch evidence could not be verified.")
	}
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	checkedAt := ""
	if !evidence.CheckedAt.IsZero() {
		checkedAt = evidence.CheckedAt.UTC().Format(time.RFC3339Nano)
	}
	return &githubdpb.GetProtectedBranchEvidenceResponse{Qualified: evidence.Qualified, Reason: evidence.Reason, Profile: evidence.Profile,
		InstallationId: evidence.InstallationID, RepositoryId: evidence.RepositoryID, RepoFullName: evidence.Repository,
		Branch: evidence.Branch, CommitSha: evidence.CommitSHA, PolicyDigest: evidence.PolicyDigest, CheckedAtRfc3339: checkedAt, RequiredReviewCount: int64(evidence.RequiredReviewCount)}, nil
}
