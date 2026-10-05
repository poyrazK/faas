package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

type githubdProtectedBranchClient = githubdgrpc.ProtectedBranchEvidenceService

var _ githubdProtectedBranchClient = (*liveClient)(nil)
var _ githubdProtectedBranchClient = stubGithubdClient{}

func (stubGithubdClient) GetProtectedBranchEvidence(context.Context, string, int64, int64, string, string, string) (githubdgrpc.ProtectedBranchEvidence, error) {
	return githubdgrpc.ProtectedBranchEvidence{}, errGithubdNotReady
}

func (l *liveClient) GetProtectedBranchEvidence(ctx context.Context, accountID string, installationID, repositoryID int64, repository, branch, commitSHA string) (githubdgrpc.ProtectedBranchEvidence, error) {
	return l.c.GetProtectedBranchEvidence(ctx, accountID, installationID, repositoryID, repository, branch, commitSHA)
}
