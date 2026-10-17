package githubdgrpc

import (
	"context"
	"errors"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/ci"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CIPullRequest carries source identity only; githubd keeps provider authority.
type CIPullRequest = ci.PullRequestState

// Optional so existing non-CI service implementations remain compatible.
type CIPullRequestService interface {
	GetCIPullRequest(context.Context, string, int64, string, int64) (CIPullRequest, error)
}

func (s *Server) GetCIPullRequest(ctx context.Context, req *githubdpb.GetCIPullRequestRequest) (*githubdpb.GetCIPullRequestResponse, error) {
	service, ok := s.svc.(CIPullRequestService)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "githubd: CI pull request lookup is not configured")
	}
	start := time.Now()
	pr, err := service.GetCIPullRequest(ctx, req.GetAccountId(), req.GetInstallationId(), req.GetRepoFullName(), req.GetNumber())
	s.ops.Observe("GetCIPullRequest", time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	if !pr.Valid(req.GetNumber()) {
		return nil, status.Error(codes.Unavailable, "githubd: incomplete CI pull request state")
	}
	return &githubdpb.GetCIPullRequestResponse{Number: pr.Number, State: pr.State, Draft: &pr.Draft, HeadSha: pr.HeadSHA,
		BaseBranch: pr.BaseBranch, HeadRepositoryId: pr.HeadRepositoryID, BaseRepositoryId: pr.BaseRepositoryID,
		HeadRepository: pr.HeadRepository, BaseRepository: pr.BaseRepository, BaseSha: pr.BaseSHA}, nil
}

func (c *Client) GetCIPullRequest(ctx context.Context, account string, installation int64, repository string, number int64) (CIPullRequest, error) {
	response, err := c.cli.GetCIPullRequest(ctx, &githubdpb.GetCIPullRequestRequest{AccountId: account, InstallationId: installation, RepoFullName: repository, Number: number})
	if err != nil {
		return CIPullRequest{}, liftErr(err)
	}
	if response == nil || response.Draft == nil {
		return CIPullRequest{}, errors.New("githubdgrpc: incomplete CI pull request response")
	}
	pr := CIPullRequest{Number: response.GetNumber(), State: response.GetState(), Draft: response.GetDraft(), HeadSHA: response.GetHeadSha(),
		BaseBranch: response.GetBaseBranch(), HeadRepositoryID: response.GetHeadRepositoryId(), BaseRepositoryID: response.GetBaseRepositoryId(),
		HeadRepository: response.GetHeadRepository(), BaseRepository: response.GetBaseRepository(), BaseSHA: response.GetBaseSha()}
	if !pr.Valid(number) {
		return CIPullRequest{}, errors.New("githubdgrpc: invalid CI pull request identity")
	}
	return pr, nil
}
