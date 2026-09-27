package imaged

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// GitHubSourceRefVerifier resolves branch heads through githubd, which owns
// the installation-token cache and GitHub API credentials.
type GitHubSourceRefVerifier interface {
	GetBranchHead(ctx context.Context, accountID string, installationID int64, repo, branch string) (sha string, found bool, err error)
}

func (h *Handler) WithGitHubSourceRefVerifier(verifier GitHubSourceRefVerifier) *Handler {
	h.githubSourceRefVerifier = verifier
	return h
}

func (h *Handler) gitHubSourceRefIsStale(ctx context.Context, dep state.Deployment) (bool, error) {
	if dep.GitHubSourceRef == "" {
		return false, nil
	}
	if dep.GitHubInstallationID <= 0 || !canonicalCommitSHA(dep.CommitSHA) {
		return false, errors.New("deployment has incomplete GitHub branch provenance")
	}
	if h.githubSourceRefVerifier == nil {
		return false, errors.New("GitHub branch verifier is not configured")
	}
	repo, ok := sourceRepoFromURL(dep.SourceURL)
	if !ok {
		return false, errors.New("deployment has an invalid GitHub source URL")
	}
	app, err := h.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return false, fmt.Errorf("load source-ref app: %w", err)
	}
	if app.AccountID == "" {
		return false, errors.New("deployment app has no owning account")
	}
	head, found, err := h.githubSourceRefVerifier.GetBranchHead(ctx, app.AccountID, dep.GitHubInstallationID, repo, dep.GitHubSourceRef)
	if err != nil {
		return false, fmt.Errorf("query GitHub branch head: %w", err)
	}
	if !found {
		return true, nil
	}
	if !canonicalCommitSHA(head) {
		return false, errors.New("GitHub returned an invalid branch head")
	}
	return !strings.EqualFold(head, dep.CommitSHA), nil
}

func sourceRepoFromURL(sourceURL string) (string, bool) {
	repoAndSHA, ok := strings.CutPrefix(sourceURL, "github://")
	if !ok {
		return "", false
	}
	repo, sha, ok := strings.Cut(repoAndSHA, "@")
	if !ok || repo == "" || !canonicalCommitSHA(sha) {
		return "", false
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return repo, true
}

func canonicalCommitSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, r := range sha {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
