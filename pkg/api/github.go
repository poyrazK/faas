package api

import "context"

// ListGitHubRepositories returns the repositories visible to the account's
// durable GitHub App installation. The server resolves the installation id
// from the bearer token's account, so callers do not need browser state or a
// manually copied installation id.
func (c *Client) ListGitHubRepositories(ctx context.Context) ([]RepoResponse, error) {
	var out []RepoResponse
	return out, c.do(ctx, "GET", "/v1/github/repos", nil, &out)
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

// DisconnectGitHubConnection removes the app's repository binding while
// leaving the account-level GitHub installation available for a later bind.
func (c *Client) DisconnectGitHubConnection(ctx context.Context, slug string) (GitHubInstallStatus, error) {
	var out GitHubInstallStatus
	return out, c.do(ctx, "DELETE", "/v1/apps/"+slug+"/github", nil, &out)
}

// GetGitHubDeploymentPolicy returns the project-level GitHub deployment
// policy used by githubd for source staging and PR previews.
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
