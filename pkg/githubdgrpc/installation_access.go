package githubdgrpc

import (
	"context"
	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type InstallationAccess struct {
	AccountType         string            `json:"account_type"`
	RepositorySelection string            `json:"repository_selection"`
	Permissions         map[string]string `json:"permissions"`
}

type installationAccessService interface {
	GetInstallationAccess(context.Context, string, int64) (InstallationAccess, error)
}

func (s *Server) GetInstallationAccess(ctx context.Context, req *githubdpb.GetInstallationAccessRequest) (*githubdpb.GetInstallationAccessResponse, error) {
	svc, ok := s.svc.(installationAccessService)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "installation verification unavailable")
	}
	access, err := svc.GetInstallationAccess(ctx, req.GetAccountId(), req.GetInstallationId())
	if err != nil {
		return nil, toStatusErr(err)
	}
	return &githubdpb.GetInstallationAccessResponse{AccountType: access.AccountType, RepositorySelection: access.RepositorySelection, Permissions: access.Permissions}, nil
}
func (c *Client) GetInstallationAccess(ctx context.Context, accountID string, installationID int64) (InstallationAccess, error) {
	r, err := c.cli.GetInstallationAccess(ctx, &githubdpb.GetInstallationAccessRequest{AccountId: accountID, InstallationId: installationID})
	if err != nil {
		return InstallationAccess{}, liftErr(err)
	}
	return InstallationAccess{AccountType: r.GetAccountType(), RepositorySelection: r.GetRepositorySelection(), Permissions: r.GetPermissions()}, nil
}
