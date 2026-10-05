package githubdgrpc

import (
	"context"
	"encoding/json"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitapproval"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ReviewedMergeEvidenceService interface {
	GetReviewedMergeEvidence(context.Context, string, int64, int64, string, string, string) (gitapproval.MergeEvidence, error)
}

func (s *Server) GetReviewedMergeEvidence(ctx context.Context, req *githubdpb.GetProtectedBranchEvidenceRequest) (*githubdpb.GetReviewedMergeEvidenceResponse, error) {
	service, ok := s.svc.(ReviewedMergeEvidenceService)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "githubd: reviewed merge evidence is not configured")
	}
	start := time.Now()
	evidence, err := service.GetReviewedMergeEvidence(ctx, req.GetAccountId(), req.GetInstallationId(), req.GetRepositoryId(), req.GetRepoFullName(), req.GetBranch(), req.GetCommitSha())
	if err == nil && evidence.Qualified && !evidence.ValidFor(req.GetInstallationId(), req.GetRepositoryId(), req.GetRepoFullName(), req.GetBranch(), req.GetCommitSha(), time.Now().UTC(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) {
		err = api.ErrSourceRefUnavailable("Reviewed merge evidence could not be verified.")
	}
	s.ops.Observe("GetReviewedMergeEvidence", time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	raw, err := json.Marshal(evidence)
	if err != nil || len(raw) > api.EnvironmentGitApprovalEvidenceMaxBytes {
		return nil, toStatusErr(api.ErrSourceRefUnavailable("Reviewed merge evidence exceeds the supported bound."))
	}
	return &githubdpb.GetReviewedMergeEvidenceResponse{EvidenceJson: raw}, nil
}

func (c *Client) GetReviewedMergeEvidence(ctx context.Context, accountID string, installationID, repositoryID int64, repository, branch, sha string) (gitapproval.MergeEvidence, error) {
	response, err := c.cli.GetReviewedMergeEvidence(ctx, &githubdpb.GetProtectedBranchEvidenceRequest{AccountId: accountID,
		InstallationId: installationID, RepositoryId: repositoryID, RepoFullName: repository, Branch: branch, CommitSha: sha})
	if err != nil {
		return gitapproval.MergeEvidence{}, liftErr(err)
	}
	var evidence gitapproval.MergeEvidence
	raw := response.GetEvidenceJson()
	if len(raw) == 0 || len(raw) > api.EnvironmentGitApprovalEvidenceMaxBytes || json.Unmarshal(raw, &evidence) != nil ||
		evidence.Qualified && !evidence.ValidFor(installationID, repositoryID, repository, branch, sha, time.Now(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) {
		return gitapproval.MergeEvidence{}, api.ErrSourceRefUnavailable("Reviewed merge evidence could not be verified.")
	}
	return evidence, nil
}
