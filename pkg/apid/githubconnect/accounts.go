package githubconnect

import (
	"context"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

type AccountStore interface {
	GitHubConnectionVerifications(context.Context, string) (map[int64]state.GitHubConnectionVerification, error)
	RecordGitHubConnectionVerification(context.Context, string, int64, state.GitHubConnectionVerification) error
	ListGitHubInstallationsForAccount(context.Context, string) ([]state.GitHubInstall, error)
	DisconnectGitHubAccountInstallation(context.Context, string, int64) error
	GitHubInstallForAccountInstallation(context.Context, string, int64) (state.GitHubInstall, error)
}
type AccessClient interface {
	GetInstallationAccess(context.Context, string, int64) (githubdgrpc.InstallationAccess, error)
	ListInstallableRepos(context.Context, string, int64) ([]githubdgrpc.Repo, error)
}
type Connection struct {
	Access          *state.GitHubConnectionVerification `json:"access,omitempty"`
	InstallationID  int64                               `json:"installation_id"`
	GitHubLogin     string                              `json:"github_login"`
	LastVerifiedAt  *time.Time                          `json:"last_verified_at,omitempty"`
	Health          string                              `json:"health"`
	RepositoryCount int                                 `json:"repository_count"`
}
type Verification struct {
	githubdgrpc.InstallationAccess
	VerifiedAt      time.Time `json:"verified_at"`
	RepositoryCount int       `json:"repository_count"`
}

func List(ctx context.Context, store AccountStore, accountID string) ([]Connection, error) {
	installs, err := store.ListGitHubInstallationsForAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	snapshots, err := store.GitHubConnectionVerifications(ctx, accountID)
	if err != nil {
		return nil, err
	}
	result := make([]Connection, 0, len(installs))
	for _, i := range installs {
		health := "unknown"
		if i.LastReconciledAt != nil {
			health = "healthy"
		}
		if i.LastReconcileError != "" {
			health = "degraded"
		}
		result = append(result, Connection{InstallationID: i.InstallationID, GitHubLogin: i.AuditGithubLogin, LastVerifiedAt: i.LastReconciledAt, Health: health, RepositoryCount: i.LastReconcileRepositoryCount})
		if snapshot, ok := snapshots[i.InstallationID]; ok {
			result[len(result)-1].Access = &snapshot
			if i.LastReconciledAt == nil || snapshot.VerifiedAt.After(*i.LastReconciledAt) {
				result[len(result)-1].LastVerifiedAt = &snapshot.VerifiedAt
				result[len(result)-1].RepositoryCount = snapshot.RepositoryCount
				result[len(result)-1].Health = "healthy"
			}
		}
	}
	return result, nil
}
func Verify(ctx context.Context, store AccountStore, client AccessClient, accountID string, installID int64) (Verification, error) {
	if _, err := store.GitHubInstallForAccountInstallation(ctx, accountID, installID); err != nil {
		return Verification{}, err
	}
	access, err := client.GetInstallationAccess(ctx, accountID, installID)
	if err != nil {
		return Verification{}, err
	}
	repos, err := client.ListInstallableRepos(ctx, accountID, installID)
	if err != nil {
		return Verification{}, err
	}
	v := Verification{InstallationAccess: access, VerifiedAt: time.Now().UTC(), RepositoryCount: len(repos)}
	if err := store.RecordGitHubConnectionVerification(ctx, accountID, installID, state.GitHubConnectionVerification{AccountType: access.AccountType, RepositorySelection: access.RepositorySelection, Permissions: access.Permissions, RepositoryCount: len(repos), VerifiedAt: v.VerifiedAt}); err != nil {
		return Verification{}, err
	}
	return v, nil
}
