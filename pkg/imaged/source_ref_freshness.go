package imaged

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	repo, sourceSHA, ok := sourceRepoFromURL(dep.SourceURL)
	if !ok || !strings.EqualFold(sourceSHA, dep.CommitSHA) {
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

func sourceRepoFromURL(sourceURL string) (string, string, bool) {
	repoAndSHA, ok := strings.CutPrefix(sourceURL, "github://")
	if ok {
		repo, sha, ok := strings.Cut(repoAndSHA, "@")
		if !ok || !validGitHubRepoFullName(repo) || !canonicalCommitSHA(sha) {
			return "", "", false
		}
		return repo, sha, true
	}
	parsed, err := url.Parse(sourceURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "codeload.github.com" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) != 4 || segments[2] != "tar.gz" || !canonicalCommitSHA(segments[3]) {
		return "", "", false
	}
	repo := segments[0] + "/" + segments[1]
	if !validGitHubRepoFullName(repo) {
		return "", "", false
	}
	return repo, segments[3], true
}

func validGitHubRepoFullName(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/")
}

func canonicalCommitSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, r := range sha {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
