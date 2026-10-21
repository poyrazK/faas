package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type CIChangedFilesClient interface {
	CIChangedFiles(context.Context, int64, string, string, string) (githubdgrpc.CIChangedFiles, error)
}

type httpCIChangedFiles struct {
	tokens *TokenCache
	client HTTPClient
}

func NewHTTPCIChangedFiles(tokens *TokenCache, client HTTPClient) CIChangedFilesClient {
	if client == nil {
		client = NewHTTPClient()
	}
	return &httpCIChangedFiles{tokens: tokens, client: client}
}

func (s *RealService) GetCIChangedFiles(ctx context.Context, account string, installation int64, repository, base, head string) (githubdgrpc.CIChangedFiles, error) {
	if account == "" || installation <= 0 || !validCIComparison(repository, base, head) {
		return ci.ChangedFiles{}, api.ErrValidation("CI comparison requires account, installation, repository and exact commit SHAs.")
	}
	inst, err := s.installForAccount(ctx, account, installation)
	if errors.Is(err, state.ErrNotFound) || err == nil && (inst.AccountID != account || inst.InstallationID != installation) {
		return ci.ChangedFiles{}, api.ErrGitHubInstallNotFound()
	}
	if err != nil || s.CIChanges == nil {
		return ci.ChangedFiles{}, api.ErrSourceRefUnavailable("CI comparison is unavailable.")
	}
	changes, err := s.CIChanges.CIChangedFiles(ctx, installation, repository, base, head)
	if err != nil || !changes.Valid(base, head) {
		return ci.ChangedFiles{}, api.ErrSourceRefUnavailable("CI comparison is incomplete.")
	}
	return changes, nil
}

func validCIComparison(repository, base, head string) bool {
	return validCIPullRequestInput(repository, 1) && ci.ValidCommitSHA(base) && ci.ValidCommitSHA(head) && base != strings.Repeat("0", 40) && head != strings.Repeat("0", 40)
}

func (c *httpCIChangedFiles) CIChangedFiles(ctx context.Context, installation int64, repository, base, head string) (ci.ChangedFiles, error) {
	failed := func() (ci.ChangedFiles, error) {
		return ci.ChangedFiles{}, api.ErrSourceRefUnavailable("CI comparison provider is unavailable or incomplete.")
	}
	if c.tokens == nil || installation <= 0 || !validCIComparison(repository, base, head) {
		return failed()
	}
	token, err := c.tokens.Token(ctx, installation)
	if err != nil {
		return failed()
	}
	endpoint := fmt.Sprintf("%s/repos/%s/compare/%s...%s?per_page=%d&page=1", GitHubAPI, repository, base, head, api.CIAutomationCompareMaxCommits)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return failed()
	}
	setGitHubHeaders(req, token)
	resp, err := c.client.Do(req)
	if err != nil || resp == nil || resp.Body == nil {
		return failed()
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Link") != "" {
		return failed()
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, api.CIAutomationCompareMaxBytes+1))
	if err != nil || len(raw) > api.CIAutomationCompareMaxBytes {
		return failed()
	}
	changes, err := decodeCIChangedFiles(raw, base, head)
	if err != nil {
		return failed()
	}
	return changes, nil
}

func decodeCIChangedFiles(raw []byte, base, head string) (ci.ChangedFiles, error) {
	var payload struct {
		Status       string `json:"status"`
		TotalCommits *int   `json:"total_commits"`
		Base         struct {
			SHA string `json:"sha"`
		} `json:"base_commit"`
		MergeBase struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
		Files *[]struct {
			Path         string `json:"filename"`
			PreviousPath string `json:"previous_filename"`
			Status       string `json:"status"`
		} `json:"files"`
	}
	if !utf8.Valid(raw) || uniqueCIPullRequestJSON(raw) != nil || json.Unmarshal(raw, &payload) != nil || payload.Files == nil || payload.TotalCommits == nil ||
		payload.Base.SHA != base || *payload.TotalCommits != len(payload.Commits) || len(payload.Commits) >= api.CIAutomationCompareMaxCommits {
		return ci.ChangedFiles{}, ci.ErrGitHubUnavailable
	}
	// A behind/diverged push cannot use this three-dot comparison to skip. PR
	// callers may use a diverged comparison with its explicit merge-base pin.
	switch payload.Status {
	case "ahead", "diverged":
		if len(payload.Commits) == 0 || payload.Commits[len(payload.Commits)-1].SHA != head {
			return ci.ChangedFiles{}, ci.ErrGitHubUnavailable
		}
	case "identical":
		if base != head || len(payload.Commits) != 0 || len(*payload.Files) != 0 || payload.MergeBase.SHA != base {
			return ci.ChangedFiles{}, ci.ErrGitHubUnavailable
		}
	default:
		return ci.ChangedFiles{}, ci.ErrGitHubUnavailable
	}
	changes := ci.ChangedFiles{BaseSHA: base, HeadSHA: head, MergeBaseSHA: payload.MergeBase.SHA, Complete: true}
	for _, file := range *payload.Files {
		changes.Files = append(changes.Files, ci.ChangedFile{Path: file.Path, PreviousPath: file.PreviousPath, Status: file.Status})
	}
	if !changes.Valid(base, head) {
		return ci.ChangedFiles{}, ci.ErrGitHubUnavailable
	}
	return changes, nil
}
