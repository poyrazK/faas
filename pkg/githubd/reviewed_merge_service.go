package githubd

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitapproval"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

var _ githubdgrpc.ReviewedMergeEvidenceService = (*RealService)(nil)

func (s *RealService) GetReviewedMergeEvidence(ctx context.Context, accountID string, installationID, repositoryID int64, repository, branch, sha string) (gitapproval.MergeEvidence, error) {
	if accountID == "" || !validProtectedBranchRequest(installationID, repositoryID, repository, branch, sha) {
		return gitapproval.MergeEvidence{}, api.ErrValidation("Reviewed merge evidence requires an exact repository, branch and commit.")
	}
	inst, err := s.installForAccount(ctx, accountID, installationID)
	if errors.Is(err, state.ErrNotFound) || err == nil && (inst.AccountID != accountID || inst.InstallationID != installationID) {
		return gitapproval.MergeEvidence{}, api.ErrGitHubInstallNotFound()
	}
	if err != nil || s.ReviewedMerges == nil {
		return gitapproval.MergeEvidence{}, api.ErrSourceRefUnavailable("Reviewed merge evidence is unavailable.")
	}
	evidence, err := s.ReviewedMerges.ReviewedMergeEvidence(ctx, installationID, repositoryID, repository, branch, sha)
	if err != nil {
		return gitapproval.MergeEvidence{}, api.ErrSourceRefUnavailable("Could not verify the merged pull request and reviews.")
	}
	return evidence, nil
}
