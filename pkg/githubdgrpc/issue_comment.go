package githubdgrpc

import (
	"context"
	"errors"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IssueComment is the comment UpsertIssueComment wrote. Created is false
// when an existing comment carrying the marker was updated.
type IssueComment struct {
	ID      int64
	URL     string
	Created bool
}

// IssueCommentService is optional, so existing service implementations
// without agent deliveries remain compatible (ADR-20261027090010 §8). Errors carry
// gRPC codes: NotFound, FailedPrecondition, Unavailable or Unknown.
type IssueCommentService interface {
	UpsertIssueComment(ctx context.Context, account string, installation int64, repository string, number int64, markerKey, body string) (IssueComment, error)
}

func (s *Server) UpsertIssueComment(ctx context.Context, req *githubdpb.UpsertIssueCommentRequest) (*githubdpb.UpsertIssueCommentResponse, error) {
	service, ok := s.svc.(IssueCommentService)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "githubd: issue comments are not configured")
	}
	start := time.Now()
	comment, err := service.UpsertIssueComment(ctx, req.GetAccountId(), req.GetInstallationId(), req.GetRepoFullName(),
		req.GetNumber(), req.GetMarkerKey(), req.GetBody())
	s.ops.Observe("UpsertIssueComment", time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.UpsertIssueCommentResponse{CommentId: comment.ID, HtmlUrl: comment.URL, Created: comment.Created}, nil
}

// UpsertIssueComment keeps the gRPC status of an error, so the caller can
// tell a refusal from a write that may have landed.
func (c *Client) UpsertIssueComment(ctx context.Context, account string, installation int64, repository string, number int64, markerKey, body string) (IssueComment, error) {
	resp, err := c.cli.UpsertIssueComment(ctx, &githubdpb.UpsertIssueCommentRequest{AccountId: account, InstallationId: installation,
		RepoFullName: repository, Number: number, MarkerKey: markerKey, Body: body})
	if err != nil {
		return IssueComment{}, err
	}
	if resp.GetCommentId() <= 0 {
		return IssueComment{}, status.Error(codes.Unknown, errors.New("githubdgrpc: UpsertIssueComment returned no comment").Error())
	}
	return IssueComment{ID: resp.GetCommentId(), URL: resp.GetHtmlUrl(), Created: resp.GetCreated()}, nil
}
