package githubd

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"net/http"
)

func (s *RealService) GetInstallationAccess(ctx context.Context, accountID string, installationID int64) (githubdgrpc.InstallationAccess, error) {
	if accountID == "" || installationID <= 0 {
		return githubdgrpc.InstallationAccess{}, api.NewProblem(404, "github_install_not_found", "GitHub connection not found", "Choose a connected installation.")
	}
	if _, err := s.installForAccount(ctx, accountID, installationID); err != nil {
		return githubdgrpc.InstallationAccess{}, api.NewProblem(http.StatusNotFound, "github_install_not_found", "GitHub connection not found", "Reconnect GitHub in Settings.")
	}
	if s.Auth == nil {
		return githubdgrpc.InstallationAccess{}, api.ErrCapacity("GitHub verification unavailable")
	}
	inst, verified, err := s.Auth.VerifyInstallation(ctx, installationID, "")
	if err != nil {
		return githubdgrpc.InstallationAccess{}, err
	}
	if !verified {
		return githubdgrpc.InstallationAccess{}, api.NewProblem(http.StatusNotFound, "github_install_not_found", "GitHub connection not found", "Reconnect GitHub in Settings.")
	}
	return githubdgrpc.InstallationAccess{AccountType: inst.Account.Type, RepositorySelection: inst.RepositorySelection, Permissions: inst.Permissions}, nil
}
