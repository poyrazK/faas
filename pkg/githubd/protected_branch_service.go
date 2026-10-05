package githubd

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

var _ githubdgrpc.ProtectedBranchEvidenceService = (*RealService)(nil)

func (s *RealService) GetProtectedBranchEvidence(ctx context.Context, accountID string, installationID, repositoryID int64, repository, branch, commitSHA string) (githubdgrpc.ProtectedBranchEvidence, error) {
	if accountID == "" || !validProtectedBranchRequest(installationID, repositoryID, repository, branch, commitSHA) {
		return githubdgrpc.ProtectedBranchEvidence{}, api.ErrValidation("Protected branch evidence requires an exact repository, branch and commit.")
	}
	inst, err := s.installForAccount(ctx, accountID, installationID)
	if errors.Is(err, state.ErrNotFound) || err == nil && (inst.AccountID != accountID || inst.InstallationID != installationID) {
		return githubdgrpc.ProtectedBranchEvidence{}, api.ErrGitHubInstallNotFound()
	}
	if err != nil || s.ProtectedBranches == nil {
		return githubdgrpc.ProtectedBranchEvidence{}, api.ErrSourceRefUnavailable("Protected branch evidence is unavailable.")
	}
	evidence, err := s.ProtectedBranches.ProtectedBranchEvidence(ctx, installationID, repositoryID, repository, branch, commitSHA)
	if err != nil {
		return githubdgrpc.ProtectedBranchEvidence{}, api.ErrSourceRefUnavailable("Could not verify the protected branch policy.")
	}
	return evidence, nil
}
