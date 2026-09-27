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

// PullRequestSnapshot is the current GitHub state, independent of webhook
// delivery order. HeadRepoFullName identifies a same-repository PR.
type PullRequestSnapshot struct {
	State            string
	HeadSHA          string
	HeadRepoFullName string
}

type PullRequestCurrentClient interface {
	CurrentPullRequest(ctx context.Context, installationID int64, repoFullName string, number int) (PullRequestSnapshot, error)
}

var ErrPullRequestUnavailable = errors.New("githubd: current pull request unavailable")

type httpCurrentPullRequests struct {
	tokens *TokenCache
	client HTTPClient
}

func NewHTTPCurrentPullRequests(tokens *TokenCache, client HTTPClient) PullRequestCurrentClient {
	if client == nil {
		client = NewHTTPClient()
	}
	return &httpCurrentPullRequests{tokens: tokens, client: client}
}

func NewUnavailableCurrentPullRequests() PullRequestCurrentClient {
	return unavailableCurrentPullRequests{}
}

type unavailableCurrentPullRequests struct{}

func (unavailableCurrentPullRequests) CurrentPullRequest(context.Context, int64, string, int) (PullRequestSnapshot, error) {
	return PullRequestSnapshot{}, ErrPullRequestUnavailable
}

func (c *httpCurrentPullRequests) CurrentPullRequest(ctx context.Context, installationID int64, repoFullName string, number int) (PullRequestSnapshot, error) {
	owner, repo, ok := strings.Cut(repoFullName, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") || number <= 0 || c.tokens == nil {
		return PullRequestSnapshot{}, ErrPullRequestUnavailable
	}
	token, err := c.tokens.Token(ctx, installationID)
	if err != nil {
		return PullRequestSnapshot{}, fmt.Errorf("%w: installation token: %v", ErrPullRequestUnavailable, err)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", GitHubAPI,
		url.PathEscape(owner), url.PathEscape(repo), number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return PullRequestSnapshot{}, fmt.Errorf("%w: request: %v", ErrPullRequestUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return PullRequestSnapshot{}, fmt.Errorf("%w: request: %v", ErrPullRequestUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return PullRequestSnapshot{}, fmt.Errorf("%w: GitHub status %d", ErrPullRequestUnavailable, resp.StatusCode)
	}
	var payload struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Head   struct {
			SHA  string `json:"sha"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload); err != nil {
		return PullRequestSnapshot{}, fmt.Errorf("%w: decode response: %v", ErrPullRequestUnavailable, err)
	}
	if payload.Number != number || (payload.State != "open" && payload.State != "closed") ||
		(payload.State == "open" && (!isCanonicalCommitSHA(payload.Head.SHA) || payload.Head.Repo.FullName == "")) {
		return PullRequestSnapshot{}, fmt.Errorf("%w: invalid response", ErrPullRequestUnavailable)
	}
	return PullRequestSnapshot{State: payload.State, HeadSHA: payload.Head.SHA,
		HeadRepoFullName: payload.Head.Repo.FullName}, nil
}

func (s *Service) currentPullRequestDelivery(ctx context.Context, installationID int64, ev PullRequestEvent) (bool, error) {
	if s.PullRequests == nil {
		return true, nil // legacy test rigs; production wires a fail-closed client
	}
	current, err := s.PullRequests.CurrentPullRequest(ctx, installationID, ev.Repository.FullName, ev.Number)
	if err != nil {
		return false, err
	}
	stale := ev.Action == PullRequestActionClosed && current.State != "closed"
	if ev.Action != PullRequestActionClosed {
		stale = current.State != "open" ||
			!strings.EqualFold(current.HeadSHA, ev.PullRequest.HeadSHA) ||
			!strings.EqualFold(current.HeadRepoFullName, ev.Repository.FullName)
	}
	if stale {
		s.Log.Info("githubd: ignore superseded PR delivery", "repo", ev.Repository.FullName,
			"pr_number", ev.Number, "action", ev.Action, "event_sha", ev.PullRequest.HeadSHA,
			"current_state", current.State, "current_sha", current.HeadSHA)
		return false, nil
	}
	return true, nil
}
