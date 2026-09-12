package api

import "context"

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
