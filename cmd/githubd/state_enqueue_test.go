package main

import (
	"context"
	"testing"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/state"
)

type captureApidBridgeClient struct {
	request *githubdpb.EnqueueBuildRequest
}

func (c *captureApidBridgeClient) EnqueueBuild(_ context.Context, req *githubdpb.EnqueueBuildRequest) (*githubdpb.EnqueueBuildResponse, error) {
	c.request = req
	return &githubdpb.EnqueueBuildResponse{BuildId: "build-1", DeploymentId: "deployment-1"}, nil
}

func (*captureApidBridgeClient) Close() error { return nil }

func TestApidEnqueuerForwardsBranchPromotionProvenance(t *testing.T) {
	client := &captureApidBridgeClient{}
	enqueuer := NewApidEnqueuer(client, nil)
	_, err := enqueuer.Enqueue(context.Background(), githubd.BuildSpec{
		App:         state.App{ID: "app-1", AccountID: "account-1"},
		CommitSHA:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourcePath:  "/var/lib/faas/githubd/build-sources/account-1/app-1/source.tar.gz",
		SourceURL:   "https://codeload.github.com/owner/repo/tar.gz/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceBytes: 1, RepoFullName: "owner/repo", Ref: "refs/heads/main", Branch: "main",
		GitHubSourceRef: "main", GitHubInstallationID: 42,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if client.request == nil {
		t.Fatal("EnqueueBuild was not called")
	}
	if client.request.GithubSourceRef != "main" || client.request.GithubInstallationId != 42 {
		t.Fatalf("wire branch provenance = (%q, %d), want (main, 42)", client.request.GithubSourceRef, client.request.GithubInstallationId)
	}
}
