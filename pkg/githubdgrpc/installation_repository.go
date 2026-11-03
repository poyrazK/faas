package githubdgrpc

import (
	"context"
	"errors"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InstallationRepository is a repository an installation sees, with its
// immutable GitHub IDs.
type InstallationRepository struct {
	ID       int64
	OwnerID  int64
	FullName string
}

// InstallationRepositoryService is optional so existing service
// implementations remain compatible (ADR-20261101160300, amendment 2).
type InstallationRepositoryService interface {
	GetInstallationRepository(ctx context.Context, accountID string, installationID int64, repoFullName string) (repo InstallationRepository, found bool, err error)
}

// ErrInstallationRepositoryUnsupported reports a githubd from before
// GetInstallationRepository; callers fall back to listing the
// installation's repositories.
var ErrInstallationRepositoryUnsupported = errors.New("githubdgrpc: installation repository lookup unsupported")

func (s *Server) GetInstallationRepository(ctx context.Context, req *githubdpb.GetInstallationRepositoryRequest) (*githubdpb.GetInstallationRepositoryResponse, error) {
	service, ok := s.svc.(InstallationRepositoryService)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "githubd: installation repository lookup is not configured")
	}
	start := time.Now()
	repo, found, err := service.GetInstallationRepository(ctx, req.GetAccountId(), req.GetInstallationId(), req.GetRepoFullName())
	s.ops.Observe("GetInstallationRepository", time.Since(start), err)
	if err != nil {
		return nil, toStatusErr(err)
	}
	if !found {
		return nil, status.Error(codes.NotFound, "githubd: the installation sees no repository of that name")
	}
	return &githubdpb.GetInstallationRepositoryResponse{RepoId: repo.ID, OwnerId: repo.OwnerID, FullName: repo.FullName}, nil
}

// GetInstallationRepository returns found=false when the installation sees
// no repository of that name, and ErrInstallationRepositoryUnsupported from
// a githubd without the RPC.
func (c *Client) GetInstallationRepository(ctx context.Context, accountID string, installationID int64, repoFullName string) (InstallationRepository, bool, error) {
	resp, err := c.cli.GetInstallationRepository(ctx, &githubdpb.GetInstallationRepositoryRequest{
		AccountId:      accountID,
		InstallationId: installationID,
		RepoFullName:   repoFullName,
	})
	switch status.Code(err) {
	case codes.OK:
	case codes.NotFound:
		return InstallationRepository{}, false, nil
	case codes.Unimplemented:
		return InstallationRepository{}, false, ErrInstallationRepositoryUnsupported
	default:
		return InstallationRepository{}, false, liftErr(err)
	}
	if resp.GetRepoId() <= 0 || resp.GetFullName() == "" {
		return InstallationRepository{}, false, errors.New("githubdgrpc: incomplete installation repository response")
	}
	return InstallationRepository{ID: resp.GetRepoId(), OwnerID: resp.GetOwnerId(), FullName: resp.GetFullName()}, true, nil
}
