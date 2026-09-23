package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitfetch"
	"github.com/onebox-faas/faas/pkg/oci"
)

// Sentinels the HTTP layer maps onto stable RFC 7807 codes.
var (
	// ErrRepoNotFound covers a missing repository, a private one, and a ref
	// that does not exist. Preflight deliberately does not distinguish them:
	// telling an anonymous caller that a private repo exists is a disclosure.
	ErrRepoNotFound = errors.New("preflight: repository not found")
	// ErrUpstreamRateLimited means GitHub throttled us, not the caller.
	ErrUpstreamRateLimited = errors.New("preflight: upstream rate limited")
	// ErrSourceTooLarge means the archive exceeded the inspection budget.
	ErrSourceTooLarge = errors.New("preflight: source too large")
)

// resolveBodyLimit bounds the commit-lookup response. Only one field is read
// from it, so a large body is a signal to stop rather than something to parse.
const resolveBodyLimit = 1 << 20

// Checker runs static preflight checks against public GitHub repositories.
//
// The HTTP client is the SSRF-guarded egress client from pkg/oci, which
// refuses to dial RFC1918, loopback, link-local and metadata addresses at dial
// time. That is the second line of defence; the first is ParseSource, which
// means no caller-supplied URL is ever fetched in the first place.
type Checker struct {
	workDir         string
	apiBaseURL      string
	codeloadBaseURL string
	httpClient      *http.Client
	// token is optional. Anonymous GitHub API calls are limited to 60 per
	// hour per IP, which a public endpoint will exhaust; operators may supply
	// a read-only token to raise it.
	token           string
	maxArchiveBytes int64
	maxTotalBytes   int64
}

// NewChecker builds a production checker. Anonymous callers get the Free plan
// source budget, which is the correct ceiling for an unauthenticated check.
func NewChecker(workDir string) *Checker {
	client := oci.NewEgressHTTPClient()
	client.Timeout = 20 * time.Second
	compressed := int64(api.MustLimitsFor(api.PlanFree).SourceTarballMaxMB) * 1024 * 1024
	return &Checker{
		workDir:         workDir,
		apiBaseURL:      "https://api.github.com",
		codeloadBaseURL: "https://codeload.github.com",
		httpClient:      client,
		maxArchiveBytes: compressed,
		maxTotalBytes:   compressed * 5 / 2,
	}
}

// WithToken supplies an optional read-only GitHub token to raise the anonymous
// API rate limit. The token is used for upstream calls and never logged.
func (c *Checker) WithToken(token string) *Checker {
	c.token = token
	return c
}

// Check resolves the source to a commit, fetches that tree, and assesses it.
func (c *Checker) Check(ctx context.Context, src Source) (Report, error) {
	sha, err := c.resolveCommit(ctx, src)
	if err != nil {
		return Report{}, err
	}
	fetcher := gitfetch.NewHTTPWithBase(c.workDir, c.codeloadBaseURL, c.httpClient, c.maxArchiveBytes, c.maxTotalBytes)
	tree, err := fetcher.Fetch(ctx, src.FullName(), sha, c.token)
	if err != nil {
		return Report{}, translateFetchError(err)
	}
	defer func() { _ = tree.Close() }()

	verdict, err := Assess(tree.FS())
	if err != nil {
		return Report{}, err
	}
	return Report{
		Source:      src,
		CommitSHA:   sha,
		Verdict:     verdict,
		PlanBudgets: PlanBudgets(),
		CheckedAt:   time.Now().UTC(),
	}, nil
}

// resolveCommit turns a ref into a commit SHA. The URL is built from already
// validated fields, never from caller input.
func (c *Checker) resolveCommit(ctx context.Context, src Source) (string, error) {
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/commits/%s", c.apiBaseURL, src.Owner, src.Repo, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("preflight resolve request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gregale-preflight/1.0")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("preflight resolve commit: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, resolveBodyLimit))
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", ErrRepoNotFound
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusTooManyRequests:
		return "", ErrUpstreamRateLimited
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("preflight resolve commit: upstream status %d", resp.StatusCode)
	}

	var payload struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, resolveBodyLimit)).Decode(&payload); err != nil {
		return "", fmt.Errorf("preflight decode commit: %w", err)
	}
	if payload.SHA == "" {
		return "", ErrRepoNotFound
	}
	return payload.SHA, nil
}

func translateFetchError(err error) error {
	switch {
	case errors.Is(err, gitfetch.ErrNotFound), errors.Is(err, gitfetch.ErrUnauthorized):
		return ErrRepoNotFound
	case errors.Is(err, gitfetch.ErrArchiveTooLarge):
		return ErrSourceTooLarge
	case errors.Is(err, oci.ErrImageEgressDenied):
		return fmt.Errorf("preflight fetch source: %w", ErrInvalidSource)
	default:
		return fmt.Errorf("preflight fetch source: %w", err)
	}
}
