// Public-repository source-ref streamer (ADR-20261126164847). It reads a
// repository GitHub reports as public, with no account or installation, so a
// customer can deploy any public repository, Gregale starters included.
//
// Every call:
//   - GET /repos/{owner}/{name}: 200 with private=false, visibility absent or
//     "public", and the requested full name (case folded). Anything else is
//     githubd.ErrRepositoryNotPublic, whether the repository is private,
//     missing or renamed.
//   - Resolve the ref to a full commit SHA and fetch the codeload archive by
//     that SHA, through sourceRefStreamer's helpers and under the same cap.
//
// The credential is FAAS_GITHUB_PUBLIC_READ_TOKEN, a fine-grained token
// restricted to public repositories (read-only). It is never an installation
// token. githubd wires the streamer only when the token is set: anonymous
// reads get 60 REST calls per hour per egress IP, a budget every account would
// share and one account could use up. The visibility check refuses a private
// repository even if the token could read one.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/gitfetch"
	"github.com/onebox-faas/faas/pkg/githubd"
)

// publicReadTokenEnv names the optional public-only GitHub token.
const publicReadTokenEnv = "FAAS_GITHUB_PUBLIC_READ_TOKEN"

// publicRepoBodyLimit bounds the repository JSON githubd decodes; GitHub's
// repository object is a few KiB.
const publicRepoBodyLimit = 1 << 20

// publicSourceRefStreamer implements githubd.PublicSourceRefStreamer.
type publicSourceRefStreamer struct {
	// archive supplies the commit-resolution and codeload helpers and the
	// upstream base URLs. Its install and token lookups are unused here.
	archive *sourceRefStreamer
	token   string
	log     *slog.Logger
}

func newPublicSourceRefStreamer(token string, httpClient *http.Client, log *slog.Logger) *publicSourceRefStreamer {
	archive := newSourceRefStreamer(nil, nil, httpClient, log)
	return &publicSourceRefStreamer{archive: archive, token: strings.TrimSpace(token), log: archive.log}
}

// StreamPublic proves the repository is public, resolves ref to a commit and
// returns the archive body capped at maxArchiveBytes. The token never leaves
// the request headers.
func (p *publicSourceRefStreamer) StreamPublic(ctx context.Context, repoFullName, ref string, maxArchiveBytes int64) (githubd.SourceRefStream, error) {
	if !isValidSourceRefRepo(repoFullName) {
		return githubd.SourceRefStream{}, fmt.Errorf("githubd: public source-ref streamer: invalid repo %q: %w", repoFullName, gitfetch.ErrBadArchive)
	}
	if !isValidSourceRefRef(ref) {
		return githubd.SourceRefStream{}, fmt.Errorf("githubd: public source-ref streamer: invalid ref %q: %w", ref, gitfetch.ErrBadArchive)
	}
	if err := p.requirePublic(ctx, repoFullName); err != nil {
		return githubd.SourceRefStream{}, err
	}
	body, resolvedSHA, err := p.archive.resolveAndFetch(ctx, repoFullName, ref, p.token)
	if err != nil {
		return githubd.SourceRefStream{}, err
	}
	p.log.Info("githubd: public source-ref fetch", "repo", repoFullName, "commit", resolvedSHA, "authenticated", p.token != "")
	return githubd.SourceRefStream{
		Body:              p.archive.capAndWrap(body, maxArchiveBytes),
		ResolvedCommitSHA: resolvedSHA,
	}, nil
}

// publicRepository is the slice of GitHub's repository object the check reads.
type publicRepository struct {
	FullName   string `json:"full_name"`
	Private    *bool  `json:"private"`
	Visibility string `json:"visibility"`
}

func (p *publicSourceRefStreamer) requirePublic(ctx context.Context, repoFullName string) error {
	endpoint := strings.TrimRight(p.archive.apiBaseURL, "/") + "/repos/" + repoFullName
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("githubd: public source-ref streamer: new repository request: %w", err)
	}
	setSourceRefAuthorization(req, p.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "onebox-faas-githubd/1.0")

	resp, err := p.archive.http.Do(req)
	if err != nil {
		return fmt.Errorf("githubd: public source-ref streamer: repository lookup: %w", err)
	}
	defer func() { _ = drainAndCloseSourceRef(resp.Body) }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("githubd: public source-ref streamer: repository lookup %d: %w", resp.StatusCode, githubd.ErrRepositoryNotPublic)
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusTooManyRequests:
		// 401 is a rejected token; 403 and 429 are GitHub's rate limits.
		p.log.Warn("githubd: public repository lookup refused",
			"status", resp.StatusCode,
			"ratelimit_remaining", resp.Header.Get("X-RateLimit-Remaining"),
			"authenticated", p.token != "")
		return fmt.Errorf("githubd: public source-ref streamer: repository lookup %d: %w", resp.StatusCode, gitfetch.ErrUnauthorized)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("githubd: public source-ref streamer: repository lookup %d: %w", resp.StatusCode, gitfetch.ErrBadArchive)
	}

	var repo publicRepository
	if err := json.NewDecoder(io.LimitReader(resp.Body, publicRepoBodyLimit)).Decode(&repo); err != nil {
		return fmt.Errorf("githubd: public source-ref streamer: decode repository: %w", gitfetch.ErrBadArchive)
	}
	if !repo.isPublic(repoFullName) {
		return fmt.Errorf("githubd: public source-ref streamer: repository is not public under the requested name: %w", githubd.ErrRepositoryNotPublic)
	}
	return nil
}

// isPublic requires an explicit private=false: a response that omits the
// field proves nothing. A different full name means the requested name now
// leads to another repository (a rename or transfer).
func (r publicRepository) isPublic(requested string) bool {
	if r.Private == nil || *r.Private {
		return false
	}
	if r.Visibility != "" && r.Visibility != "public" {
		return false
	}
	return strings.EqualFold(r.FullName, requested)
}

var _ githubd.PublicSourceRefStreamer = (*publicSourceRefStreamer)(nil)
