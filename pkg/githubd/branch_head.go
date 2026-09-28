package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// BranchHeadClient resolves the current remote head before a push webhook
// mutates project state or queues a build. Webhook delivery order is not a
// branch-order guarantee.
type BranchHeadClient interface {
	BranchHead(ctx context.Context, installationID int64, repoFullName, branch string) (string, error)
}

var ErrBranchHeadUnavailable = errors.New("githubd: branch head unavailable")
var ErrBranchHeadNotFound = errors.New("githubd: branch not found")

type httpBranchHeads struct {
	tokens *TokenCache
	client HTTPClient
}

func NewHTTPBranchHeads(tokens *TokenCache, client HTTPClient) BranchHeadClient {
	if client == nil {
		client = NewHTTPClient()
	}
	return &httpBranchHeads{tokens: tokens, client: client}
}

func NewUnavailableBranchHeads() BranchHeadClient { return unavailableBranchHeads{} }

type unavailableBranchHeads struct{}

func (unavailableBranchHeads) BranchHead(context.Context, int64, string, string) (string, error) {
	return "", ErrBranchHeadUnavailable
}

func (c *httpBranchHeads) BranchHead(ctx context.Context, installationID int64, repoFullName, branch string) (string, error) {
	owner, repo, ok := strings.Cut(repoFullName, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") || branch == "" || c.tokens == nil {
		return "", ErrBranchHeadUnavailable
	}
	token, err := c.tokens.Token(ctx, installationID)
	if err != nil {
		return "", fmt.Errorf("%w: installation token: %w", ErrBranchHeadUnavailable, err)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/branches/%s", GitHubAPI,
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("%w: request: %w", ErrBranchHeadUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: request: %w", ErrBranchHeadUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return "", ErrBranchHeadNotFound
		}
		return "", fmt.Errorf("%w: GitHub status %d", ErrBranchHeadUnavailable, resp.StatusCode)
	}
	var payload struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload); err != nil {
		return "", fmt.Errorf("%w: decode response: %w", ErrBranchHeadUnavailable, err)
	}
	if !isCanonicalCommitSHA(payload.Commit.SHA) {
		return "", fmt.Errorf("%w: invalid commit SHA", ErrBranchHeadUnavailable)
	}
	return payload.Commit.SHA, nil
}
