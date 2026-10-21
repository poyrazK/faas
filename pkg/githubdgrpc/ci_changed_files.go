package githubdgrpc

import (
	"context"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/ci"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type CIChangedFiles = ci.ChangedFiles
type CIChangedFilesService interface {
	GetCIChangedFiles(context.Context, string, int64, string, string, string) (CIChangedFiles, error)
}

func (s *Server) GetCIChangedFiles(ctx context.Context, req *githubdpb.GetCIChangedFilesRequest) (*githubdpb.GetCIChangedFilesResponse, error) {
	service, ok := s.svc.(CIChangedFilesService)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "githubd: CI comparison is not configured")
	}
	start := time.Now()
	changes, err := service.GetCIChangedFiles(ctx, req.GetAccountId(), req.GetInstallationId(), req.GetRepoFullName(), req.GetBaseSha(), req.GetHeadSha())
	s.ops.Observe("GetCIChangedFiles", time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	if !changes.Valid(req.GetBaseSha(), req.GetHeadSha()) {
		return nil, status.Error(codes.Unavailable, "githubd: incomplete CI comparison")
	}
	response := &githubdpb.GetCIChangedFilesResponse{BaseSha: changes.BaseSHA, HeadSha: changes.HeadSHA, MergeBaseSha: changes.MergeBaseSHA, Complete: true}
	for _, file := range changes.Files {
		response.Files = append(response.Files, &githubdpb.CIChangedFile{Path: file.Path, PreviousPath: file.PreviousPath, Status: file.Status})
	}
	return response, nil
}

func (c *Client) GetCIChangedFiles(ctx context.Context, account string, installation int64, repository, base, head string) (CIChangedFiles, error) {
	response, err := c.cli.GetCIChangedFiles(ctx, &githubdpb.GetCIChangedFilesRequest{AccountId: account, InstallationId: installation, RepoFullName: repository, BaseSha: base, HeadSha: head})
	if err != nil {
		return CIChangedFiles{}, liftErr(err)
	}
	changes := CIChangedFiles{BaseSHA: response.GetBaseSha(), HeadSHA: response.GetHeadSha(), MergeBaseSHA: response.GetMergeBaseSha(), Complete: response.GetComplete()}
	for _, file := range response.GetFiles() {
		changes.Files = append(changes.Files, ci.ChangedFile{Path: file.GetPath(), PreviousPath: file.GetPreviousPath(), Status: file.GetStatus()})
	}
	if !changes.Valid(base, head) {
		return CIChangedFiles{}, status.Error(codes.Unavailable, "githubd: incomplete CI comparison response")
	}
	return changes, nil
}
