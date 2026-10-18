package githubd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

// VerifyCIRepository reads durable account ownership on every call, including
// when the installation token is cached. Only githubd contacts GitHub.
func (s *RealService) VerifyCIRepository(ctx context.Context, account string, installationID, repositoryID int64, repository string) error {
	if s == nil || s.Auth == nil || s.Auth.HTTPClient == nil || s.Tokens == nil {
		return ci.ErrGitHubUnavailable
	}
	if _, err := s.installForAccount(ctx, account, installationID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return ci.ErrGitHubAccess
		}
		return ci.ErrGitHubUnavailable
	}
	token, err := s.Tokens.Token(ctx, installationID)
	if err != nil {
		return ci.ErrGitHubUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GitHubAPI+"/repos/"+repository, nil)
	if err != nil {
		return ci.ErrGitHubUnavailable
	}
	setGitHubHeaders(req, token)
	resp, err := s.Auth.HTTPClient.Do(req)
	if err != nil {
		return ci.ErrGitHubUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if err := ciGitHubStatus(resp); err != nil {
		return err
	}
	var repo struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	}
	if err := decodeGitHubJSON(resp.Body, &repo); err != nil {
		return ci.ErrGitHubUnavailable
	}
	if repo.ID != repositoryID || !strings.EqualFold(repo.FullName, repository) {
		return ci.ErrGitHubAccess
	}
	return nil
}

func ciGitHubStatus(resp *http.Response) error {
	status := resp.StatusCode
	if status == http.StatusForbidden && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "") {
		return ci.ErrGitHubUnavailable
	}
	if status == http.StatusForbidden || status == http.StatusNotFound {
		return ci.ErrGitHubAccess
	}
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return ci.ErrGitHubUnavailable
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("CI GitHub response status %d", status)
	}
	return nil
}

// WriteBlockedCICheck has its own per-run identity. It never consults app or
// deployment check mappings and can only publish action_required, not success.
// Before POST it searches by external_id, recovering a lost POST acknowledgement.
func (c *ChecksAPI) WriteBlockedCICheck(ctx context.Context, check ci.GitHubCheck) (int64, error) {
	if c == nil || c.Tokens == nil || c.HTTP == nil {
		return 0, ci.ErrGitHubUnavailable
	}
	if !check.Source.Valid() || !ci.ValidID(check.RunID) || !ci.ValidID(check.Source.BindingID) || !ci.ValidName(check.Pipeline) {
		return 0, fmt.Errorf("invalid CI Check identity")
	}
	token, err := c.Tokens.Token(ctx, check.Source.InstallationID)
	if err != nil {
		return 0, ci.ErrGitHubUnavailable
	}
	id := check.CheckID
	if id == 0 {
		id, err = c.findCICheck(ctx, token, check)
		if err != nil {
			return 0, err
		}
	}
	method, endpoint := http.MethodPost, GitHubAPI+"/repos/"+check.Source.Repository+"/check-runs"
	payload := checkRunRequest{Name: check.Name(), HeadSHA: check.Source.CommitSHA, Status: "completed", Conclusion: "action_required", ExternalID: check.ExternalID(),
		Output: &checkRunOutput{Title: "Gregale CI blocked: compute unavailable", Summary: "Compute execution is unavailable. Gregale CI recorded this commit as a blocked run. Run: " + check.RunID + "."}}
	if id > 0 {
		method, endpoint, payload.HeadSHA = http.MethodPatch, endpoint+"/"+strconv.FormatInt(id, 10), ""
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encode CI check: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return 0, fmt.Errorf("construct CI check: %w", err)
	}
	setGitHubHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, ci.ErrGitHubUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if err := ciGitHubStatus(resp); err != nil {
		return 0, err
	}
	var result checkRunResponse
	if err := decodeGitHubJSON(resp.Body, &result); err != nil {
		return 0, ci.ErrGitHubUnavailable
	}
	if result.ID <= 0 || id > 0 && result.ID != id {
		return 0, fmt.Errorf("CI check response identity mismatch")
	}
	return result.ID, nil
}

func (c *ChecksAPI) findCICheck(ctx context.Context, token string, check ci.GitHubCheck) (int64, error) {
	return c.findOwnedCICheck(ctx, token, check.Source.Repository, check.Source.CommitSHA, check.Name(), check.ExternalID())
}

func (c *ChecksAPI) findOwnedCICheck(ctx context.Context, token, repository, sha, name, externalID string) (int64, error) {
	for page := 1; page <= api.CIGitHubCheckMaxPages; page++ {
		values := url.Values{"filter": {"all"}, "check_name": {name}, "per_page": {strconv.Itoa(api.CIGitHubCheckPageSize)}, "page": {strconv.Itoa(page)}}
		endpoint := GitHubAPI + "/repos/" + repository + "/commits/" + sha + "/check-runs?" + values.Encode()
		out, err := c.listCICheckPage(ctx, token, endpoint)
		if err != nil {
			return 0, err
		}
		for _, run := range out.CheckRuns {
			if run.ID > 0 && run.Name == name && run.ExternalID == externalID && run.HeadSHA == sha {
				return run.ID, nil
			}
		}
		if len(out.CheckRuns) < api.CIGitHubCheckPageSize && out.TotalCount > page*api.CIGitHubCheckPageSize {
			return 0, ci.ErrGitHubUnavailable
		}
		if len(out.CheckRuns) < api.CIGitHubCheckPageSize {
			return 0, nil
		}
	}
	return 0, fmt.Errorf("CI check discovery exceeded page allowance")
}

type ciCheckPage struct {
	TotalCount int `json:"total_count"`
	CheckRuns  []struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		ExternalID string `json:"external_id"`
		HeadSHA    string `json:"head_sha"`
	} `json:"check_runs"`
}

func (c *ChecksAPI) listCICheckPage(ctx context.Context, token, endpoint string) (ciCheckPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ciCheckPage{}, ci.ErrGitHubUnavailable
	}
	setGitHubHeaders(req, token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ciCheckPage{}, ci.ErrGitHubUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if err := ciGitHubStatus(resp); err != nil {
		return ciCheckPage{}, err
	}
	var out ciCheckPage
	if err := decodeGitHubJSON(resp.Body, &out); err != nil {
		return out, ci.ErrGitHubUnavailable
	}
	return out, nil
}
