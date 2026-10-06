package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gitapproval"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

type githubdReviewedMergeClient = githubdgrpc.ReviewedMergeEvidenceService

var _ githubdReviewedMergeClient = (*liveClient)(nil)
var _ githubdReviewedMergeClient = stubGithubdClient{}

func (stubGithubdClient) GetReviewedMergeEvidence(context.Context, string, int64, int64, string, string, string) (gitapproval.MergeEvidence, error) {
	return gitapproval.MergeEvidence{}, errGithubdNotReady
}

func (l *liveClient) GetReviewedMergeEvidence(ctx context.Context, accountID string, installationID, repositoryID int64, repository, branch, sha string) (gitapproval.MergeEvidence, error) {
	return l.c.GetReviewedMergeEvidence(ctx, accountID, installationID, repositoryID, repository, branch, sha)
}
