package githubd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type CIPullRequestClient interface {
	CIPullRequest(context.Context, int64, string, int64) (githubdgrpc.CIPullRequest, error)
}

type httpCIPullRequests struct {
	tokens *TokenCache
	client HTTPClient
}

func NewHTTPCIPullRequests(tokens *TokenCache, client HTTPClient) CIPullRequestClient {
	if client == nil {
		client = NewHTTPClient()
	}
	return &httpCIPullRequests{tokens: tokens, client: client}
}

func (s *RealService) GetCIPullRequest(ctx context.Context, account string, installation int64, repository string, number int64) (githubdgrpc.CIPullRequest, error) {
	if account == "" || installation <= 0 || !validCIPullRequestInput(repository, number) {
		return githubdgrpc.CIPullRequest{}, api.ErrValidation("CI pull request lookup requires account, installation, repository and PR number.")
	}
	inst, err := s.installForAccount(ctx, account, installation)
	if errors.Is(err, state.ErrNotFound) || err == nil && (inst.AccountID != account || inst.InstallationID != installation) {
		return githubdgrpc.CIPullRequest{}, api.ErrGitHubInstallNotFound()
	}
	if err != nil || s.CIPullRequests == nil {
		return githubdgrpc.CIPullRequest{}, api.ErrSourceRefUnavailable("CI pull request lookup is unavailable.")
	}
	pr, err := s.CIPullRequests.CIPullRequest(ctx, installation, repository, number)
	if err != nil || !pr.Valid(number) {
		return githubdgrpc.CIPullRequest{}, api.ErrSourceRefUnavailable("Could not verify current CI pull request state.")
	}
	return pr, nil
}

func validCIPullRequestInput(repository string, number int64) bool {
	if number <= 0 || len(repository) > api.CIRepositoryMaxBytes || strings.Count(repository, "/") != 1 {
		return false
	}
	for _, part := range strings.Split(repository, "/") {
		if part == "" {
			return false
		}
		for i, r := range part {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				continue
			}
			if i == 0 || r != '.' && r != '_' && r != '-' {
				return false
			}
		}
	}
	return true
}

func (c *httpCIPullRequests) CIPullRequest(ctx context.Context, installation int64, repository string, number int64) (githubdgrpc.CIPullRequest, error) {
	unavailable := func() (githubdgrpc.CIPullRequest, error) {
		return githubdgrpc.CIPullRequest{}, api.ErrSourceRefUnavailable("CI pull request provider is unavailable.")
	}
	if c.tokens == nil || installation <= 0 || !validCIPullRequestInput(repository, number) {
		return unavailable()
	}
	token, err := c.tokens.Token(ctx, installation)
	if err != nil {
		return unavailable()
	}
	owner, repo, _ := strings.Cut(repository, "/")
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", GitHubAPI, url.PathEscape(owner), url.PathEscape(repo), number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return unavailable()
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")
	response, err := c.client.Do(req)
	if err != nil || response == nil || response.Body == nil {
		return unavailable()
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return unavailable() // 404 cannot establish a closed or inaccessible PR.
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, api.CIGitHubPullRequestMaxBytes+1))
	if err != nil || len(raw) > api.CIGitHubPullRequestMaxBytes {
		return unavailable()
	}
	return decodeCIPullRequest(raw, number)
}

func decodeCIPullRequest(raw []byte, number int64) (githubdgrpc.CIPullRequest, error) {
	var payload struct {
		Number int64  `json:"number"`
		State  string `json:"state"`
		Draft  *bool  `json:"draft"`
		Head   struct {
			SHA  string       `json:"sha"`
			Repo gitHubCIRepo `json:"repo"`
		} `json:"head"`
		Base struct {
			SHA  string       `json:"sha"`
			Ref  string       `json:"ref"`
			Repo gitHubCIRepo `json:"repo"`
		} `json:"base"`
	}
	// Bound nesting and reject duplicate/case-alias keys before decoding identity.
	if err := uniqueCIPullRequestJSON(raw); err != nil {
		return githubdgrpc.CIPullRequest{}, api.ErrSourceRefUnavailable("Invalid CI pull request provider response.")
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Draft == nil {
		return githubdgrpc.CIPullRequest{}, api.ErrSourceRefUnavailable("Incomplete CI pull request provider response.")
	}
	pr := githubdgrpc.CIPullRequest{Number: payload.Number, State: payload.State, Draft: *payload.Draft,
		HeadSHA: payload.Head.SHA, BaseSHA: payload.Base.SHA, BaseBranch: payload.Base.Ref, HeadRepositoryID: payload.Head.Repo.ID, BaseRepositoryID: payload.Base.Repo.ID,
		HeadRepository: payload.Head.Repo.FullName, BaseRepository: payload.Base.Repo.FullName}
	if !pr.Valid(number) {
		return githubdgrpc.CIPullRequest{}, api.ErrSourceRefUnavailable("Invalid CI pull request provider identity.")
	}
	return pr, nil
}

type gitHubCIRepo struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
}

func uniqueCIPullRequestJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return uniqueCIPullRequestValue(decoder, 0)
}

func uniqueCIPullRequestValue(decoder *json.Decoder, depth int) error {
	if depth > api.CIGitHubPullRequestJSONMaxDepth {
		return errors.New("CI pull request JSON is too deeply nested")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			// JSON also folds Unicode aliases such as long s and Kelvin sign.
			name = strings.ToLower(strings.ToUpper(name))
			if !ok || seen[name] {
				return errors.New("CI pull request JSON contains ambiguous keys")
			}
			seen[name] = true
		}
		if err := uniqueCIPullRequestValue(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
