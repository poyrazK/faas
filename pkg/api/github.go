package api

import (
	"context"
	"strconv"
)

// ListGitHubRepositories returns the repositories visible to the account's
// durable GitHub App installation. The server resolves the installation id
// from the bearer token's account, so callers do not need browser state or a
// manually copied installation id.
func (c *Client) ListGitHubRepositories(ctx context.Context) ([]RepoResponse, error) {
	return c.ListGitHubRepositoriesForInstallation(ctx, 0)
}

// ListGitHubRepositoriesForInstallation names the installation to list. An
// account with several installations must name one; zero takes the
// account's only installation.
func (c *Client) ListGitHubRepositoriesForInstallation(ctx context.Context, installationID int64) ([]RepoResponse, error) {
	var out []RepoResponse
	path := "/v1/github/repos"
	if installationID > 0 {
		path += "?installation_id=" + strconv.FormatInt(installationID, 10)
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetGitHubConnection returns the account-scoped GitHub installation and app
// binding projection. The endpoint is bearer-only and requires github:manage.
func (c *Client) GetGitHubConnection(ctx context.Context, slug string) (GitHubInstallStatus, error) {
	var out GitHubInstallStatus
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/github", nil, &out)
}

// BindGitHubConnection binds or rebinds an app to a repository visible to the
// account's GitHub App installation.
func (c *Client) BindGitHubConnection(ctx context.Context, slug string, req InstallBindRequest) (InstallBindResponse, error) {
	var out InstallBindResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/github/bind", req, &out)
}

// SyncGitHubConnection asks GitHub for the installation's current repository
// access and detaches the app if its bound repository disappeared.
func (c *Client) SyncGitHubConnection(ctx context.Context, slug string) (GitHubInstallStatus, error) {
	var out GitHubInstallStatus
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/github/sync", nil, &out)
}

// RetryGitHubActivity requeues recent failed webhook and Check Run activity
// for an app. The response contains aggregate counts, not queue identifiers.
func (c *Client) RetryGitHubActivity(ctx context.Context, slug string) (GitHubActivityRetryResponse, error) {
	var out GitHubActivityRetryResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/github/activity/retry", nil, &out)
}

// DisconnectGitHubConnection removes the app's repository binding while
// leaving the account-level GitHub installation available for a later bind.
func (c *Client) DisconnectGitHubConnection(ctx context.Context, slug string) (GitHubInstallStatus, error) {
	var out GitHubInstallStatus
	return out, c.do(ctx, "DELETE", "/v1/apps/"+slug+"/github", nil, &out)
}

// GetGitHubDeploymentPolicy returns the project-level deployment policy used
// for source staging, PR previews, and preview-to-production service calls.
func (c *Client) GetGitHubDeploymentPolicy(ctx context.Context, slug string) (GitHubDeploymentPolicy, error) {
	var out GitHubDeploymentPolicy
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/github/deployment-policy", nil, &out)
}

// PatchGitHubDeploymentPolicy updates the project-level GitHub deployment
// policy. Omitted fields retain their current values.
func (c *Client) PatchGitHubDeploymentPolicy(ctx context.Context, slug string, req GitHubDeploymentPolicyPatch) (GitHubDeploymentPolicy, error) {
	var out GitHubDeploymentPolicy
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug+"/github/deployment-policy", req, &out)
}
