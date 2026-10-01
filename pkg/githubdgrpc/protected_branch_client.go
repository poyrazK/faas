package githubdgrpc

import (
	"context"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func (c *Client) GetProtectedBranchEvidence(ctx context.Context, accountID string, installationID, repositoryID int64, repository, branch, commitSHA string) (ProtectedBranchEvidence, error) {
	response, err := c.cli.GetProtectedBranchEvidence(ctx, &githubdpb.GetProtectedBranchEvidenceRequest{AccountId: accountID,
		InstallationId: installationID, RepositoryId: repositoryID, RepoFullName: repository, Branch: branch, CommitSha: commitSHA})
	if err != nil {
		return ProtectedBranchEvidence{}, liftErr(err)
	}
	result := ProtectedBranchEvidence{Qualified: response.GetQualified(), Reason: response.GetReason(), Profile: response.GetProfile(),
		InstallationID: response.GetInstallationId(), RepositoryID: response.GetRepositoryId(), Repository: response.GetRepoFullName(),
		Branch: response.GetBranch(), CommitSHA: response.GetCommitSha(), PolicyDigest: response.GetPolicyDigest()}
	if response.GetCheckedAtRfc3339() != "" {
		result.CheckedAt, err = time.Parse(time.RFC3339Nano, response.GetCheckedAtRfc3339())
	}
	if err != nil || result.Qualified && !result.ValidFor(installationID, repositoryID, repository, branch, commitSHA, time.Now().UTC(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) {
		return ProtectedBranchEvidence{}, api.ErrSourceRefUnavailable("Protected branch evidence could not be verified.")
	}
	return result, nil
}
