package api

import "context"

// ListGitHubRepositories returns repositories visible to the account's
// durable GitHub App installation. The server resolves the installation id
// from the bearer token's account.
func (c *Client) ListGitHubRepositories(ctx context.Context) ([]RepoResponse, error) {
	var out []RepoResponse
	return out, c.do(ctx, "GET", "/v1/github/repos", nil, &out)
}
