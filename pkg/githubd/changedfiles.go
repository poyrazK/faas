// changedfiles.go — GitHub compare-API client for path-filtered
// build fan-out (ADR-050 §103-109).
//
// Push dispatch consumes the changed-file list between before and
// after SHAs to decide which apps in a monorepo need to rebuild.
// The naive alternative (rebuild every touched app) wastes build
// minutes on unrelated paths — for a 6-service monorepo a
// one-line README change fires 6 builds.
//
// Endpoints (no GitHub SDK; matches the rest of pkg/githubd):
//
//	GET https://api.github.com/repos/{owner}/{repo}/compare/{base}...{head}
//	Auth: Bearer <installation_token>
//	Response: { status, ahead_by, behind_by, total_commits, commits[], files[] }
//
// Truncation handling: GitHub caps the `files` array at 300 per
// page (no Link header for files; the response body is
// self-truncating) and paginates `commits` via Link when
// total_commits > commits.length. Either signal means the diff is
// too large to trust → service.HandlePushRequest maps this to the
// "rebuild all" fallback per ADR-050 §109.
//
// Retry policy: a single retry-on-429 honoring the Retry-After
// header, capped at 2 retries, with a ±10% jitter to avoid
// stampede. The retry budget lives in this file because the
// caller semantics are unique to push dispatch (must be bounded;
// infinite retries would tie up the webhook goroutine). The
// pattern is intentionally not lifted into oauth.go — no other
// githubd outbound caller is 429-aware today, and we don't want
// to sprawl a new retry helper without a second caller.
package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrTruncated is returned when GitHub's compare response is too
// large to trust (files capped at 300, or commits paginated past
// page 1). Service.HandlePushRequest maps this to the
// "rebuild all" fallback per ADR-050 §109.
//
// Also returned when the caller's `base` SHA is empty — the
// caller can't form a compare URL.
var ErrTruncated = errors.New("changedfiles: diff truncated; rebuild all")

// ErrUnavailable is returned for transport errors, non-2xx
// responses (excluding 429-retry-exhausted, which maps to
// ErrTruncated), and 4xx/5xx bodies we can't decode. Service
// maps any non-nil error to the "rebuild all" fallback too —
// conservative v1.0 posture.
var ErrUnavailable = errors.New("changedfiles: lookup failed")

// ErrBreakerOpen is returned by the wrappingChangedFiles client
// when the circuit breaker is tripped. The caller maps this to
// the "rebuild all" fallback per ADR-050 §109, the same as
// ErrTruncated / ErrUnavailable. Distinguished from those so the
// githubd_path_filter_total{breaker_open} counter can be
// incremented independently — the breaker is a separate
// observability channel from a single transient compare-API
// failure.
//
// Trip conditions: breakerFailureThreshold (3) consecutive
// non-truncation errors within breakerFailureWindow (5 min).
// Reset: a single success clears the counter; if tripped, the
// breaker auto-resets after breakerCooldown (10 min). The
// cooldown is intentionally longer than the failure window —
// a tripped breaker that resets inside the failure window
// would re-trip on the same upstream outage.
var ErrBreakerOpen = errors.New("changedfiles: breaker open")

// compareFilesCap is the documented per-page ceiling on the
// files[] array GitHub returns from compare. When the array hits
// this length we treat the diff as truncated even if Link headers
// aren't present (defense in depth: GitHub may return <300 files
// on a legitimately large diff, but a >=300 response is
// unambiguous).
const compareFilesCap = 300

// compareCommitsCap is the documented per-page ceiling on
// the commits[] array GitHub returns from compare. Per
// GitHub's REST docs the compare-API caps commits at 250
// per page; the response links to the next page via Link
// header only when total_commits > len(commits). We treat
// len(commits) >= the cap as truncated even without the Link
// header (defense in depth — a 250-commit payload that
// GitHub failed to link is still untrustworthy, and the
// conservative fallback rebuilds all).
//
// Why we don't just rely on TotalCommits > len(Commits):
// the API can return total=250, page=250 at the boundary,
// claiming the diff "fits on one page" while actually
// being truncated. The `>=` boundary check catches the
// off-by-one.
const compareCommitsCap = 250

// ChangedFilesClient returns the list of changed file paths
// between two SHAs for the named repo. The token is resolved
// per-call via TokenCache for installationID — keeps the
// install row out of the daemon wiring and naturally handles
// install-ID rotation (Option A from the plan review).
type ChangedFilesClient interface {
	ChangedFiles(ctx context.Context, installationID int64, owner, repo, base, head string) ([]string, error)
}

// NewHTTPChangedFiles constructs the production client. tokens
// resolves installation tokens; httpClient is the network seam
// (tests inject a URL-rewriting stub).
func NewHTTPChangedFiles(tokens *TokenCache, httpClient HTTPClient) ChangedFilesClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &httpChangedFiles{
		tokens:      tokens,
		httpClient:  httpClient,
		maxRetries:  2,
		baseBackoff: 200 * time.Millisecond,
	}
}

// Breaker constants for the wrappingChangedFiles circuit
// breaker (issue #432 phase 5 / ADR-050 §109). The defaults
// match the plan's risk register: 3 consecutive failures trips
// the breaker; a 5-min failure window matches the upstream
// GitHub compare-API rate-limit window (5000/h per install);
// the 10-min cooldown is intentionally longer than the failure
// window so a re-trip on the same outage doesn't double-fire.
const (
	breakerFailureThreshold = 3
	breakerFailureWindow    = 5 * time.Minute
	breakerCooldown         = 10 * time.Minute
)

// NewBreakerChangedFiles wraps inner with a circuit breaker.
// The breaker is a thin decorator — production wiring is
// NewBreakerChangedFiles(NewHTTPChangedFiles(...)). The
// returned client implements ChangedFilesClient; the breaker
// state is internal to the wrapper and not exported.
//
// now is the clock seam so tests can advance time without
// sleeping. Nil falls back to time.Now (the production path).
//
// Concurrency: a sync.Mutex guards the failure counter + the
// trippedUntil field. The inner client's transport is
// goroutine-safe (it owns the http.Client), so a single
// wrapper instance can be shared across webhook goroutines.
func NewBreakerChangedFiles(inner ChangedFilesClient, now func() time.Time) ChangedFilesClient {
	if inner == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &wrappingChangedFiles{inner: inner, now: now}
}

// wrappingChangedFiles is the breaker wrapper. It tracks
// consecutive non-truncation errors (ErrUnavailable, transport,
// 4xx/5xx) and trips after breakerFailureThreshold in
// breakerFailureWindow. A successful call resets the counter
// to zero. Once tripped, all calls return ErrBreakerOpen
// until breakerCooldown elapses, at which point the next call
// is allowed through (half-open state isn't implemented — the
// breaker is "open-or-closed", and a single success resets the
// failure count from any prior trip).
//
// ErrTruncated (the canonical "rebuild all" signal) is NOT a
// failure — the upstream is responding, the diff is just big.
// Counting truncations toward the breaker threshold would
// conflate "rate-limit / outage" with "big push to a popular
// repo" and cause the breaker to trip on every push to a busy
// monorepo.
type wrappingChangedFiles struct {
	inner        ChangedFilesClient
	now          func() time.Time
	mu           sync.Mutex
	failureCount int
	firstFailure time.Time
	trippedUntil time.Time
}

// ChangedFiles delegates to the inner client unless the
// breaker is tripped. On success the counter resets; on
// non-truncation errors the counter increments and the
// breaker may trip.
func (w *wrappingChangedFiles) ChangedFiles(
	ctx context.Context,
	installationID int64,
	owner, repo, base, head string,
) ([]string, error) {
	now := w.now()
	if w.isOpen(now) {
		return nil, ErrBreakerOpen
	}
	files, err := w.inner.ChangedFiles(ctx, installationID, owner, repo, base, head)
	w.recordOutcome(now, err)
	return files, err
}

// isOpen reports whether the breaker is tripped at t. The
// caller passes w.now() so the check uses the same clock the
// caller did for any time-based decisions. A tripped breaker
// auto-resets once the cooldown elapses — the next call is
// allowed through unconditionally; a single failure during
// the half-open window re-trips it for another full cooldown.
func (w *wrappingChangedFiles) isOpen(t time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.trippedUntil.IsZero() {
		return false
	}
	if !t.Before(w.trippedUntil) {
		// Cooldown elapsed; reset state and let the call through.
		w.trippedUntil = time.Time{}
		w.failureCount = 0
		w.firstFailure = time.Time{}
		return false
	}
	return true
}

// recordOutcome updates the breaker state after an inner
// call. Truncation is NOT a failure (the upstream is healthy;
// the diff is too big — see wrappingChangedFiles doc-comment).
// A success resets the counter regardless of the failure
// window. A failure increments the counter; if the counter
// hits breakerFailureThreshold AND all those failures fall
// within breakerFailureWindow, the breaker trips.
func (w *wrappingChangedFiles) recordOutcome(t time.Time, err error) {
	if err == nil {
		w.mu.Lock()
		w.failureCount = 0
		w.firstFailure = time.Time{}
		w.mu.Unlock()
		return
	}
	if errors.Is(err, ErrTruncated) {
		// Upstream is healthy; the diff is just big.
		// Counting toward the breaker threshold would
		// cause false trips on popular-repo pushes.
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// If the first failure was outside the failure window,
	// reset the counter so a slow-drip failure pattern
	// doesn't accumulate forever.
	if !w.firstFailure.IsZero() && t.Sub(w.firstFailure) > breakerFailureWindow {
		w.failureCount = 0
		w.firstFailure = time.Time{}
	}
	if w.firstFailure.IsZero() {
		w.firstFailure = t
	}
	w.failureCount++
	if w.failureCount >= breakerFailureThreshold {
		w.trippedUntil = t.Add(breakerCooldown)
	}
}

// httpChangedFiles is the production ChangedFilesClient. All
// fields are immutable after construction.
type httpChangedFiles struct {
	tokens      *TokenCache
	httpClient  HTTPClient
	maxRetries  int
	baseBackoff time.Duration
}

// compareResponse is the subset of GitHub's compare-API response
// we consume. Status + ahead/behind + total_commits drive
// truncation detection; commits + files drive the actual data.
// Additions beyond this shape are silently ignored by encoding/json.
type compareResponse struct {
	Status       string        `json:"status"`
	AheadBy      int           `json:"ahead_by"`
	BehindBy     int           `json:"behind_by"`
	TotalCommits int           `json:"total_commits"`
	Commits      []struct{}    `json:"commits"`
	Files        []compareFile `json:"files"`
}

// compareFile is one entry in compareResponse.files. We extract
// both the new and previous filenames so a rename from outside
// a workload's RootDir → into it still triggers a rebuild of
// the destination workload.
type compareFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Status           string `json:"status"`
}

// ChangedFiles implements ChangedFilesClient.
//
// Returns:
//
//   - All changed filenames (filename + previous_filename on
//     renames), regardless of status. The caller decides what
//     "changed" means for the filter.
//   - ErrTruncated when base is empty, commits are paginated
//     past page 1, or files hit the 300-cap.
//   - ErrUnavailable (wrapped) on transport / 4xx / 5xx /
//     retry-exhausted-non-429.
//   - A token-resolution error from tokens.Token propagates as
//     a wrapped ErrUnavailable.
func (c *httpChangedFiles) ChangedFiles(
	ctx context.Context,
	installationID int64,
	owner, repo, base, head string,
) ([]string, error) {
	if base == "" {
		// Can't form a compare URL with an empty base; map to
		// "truncated" so the caller falls back.
		return nil, ErrTruncated
	}
	if owner == "" || repo == "" || head == "" {
		return nil, fmt.Errorf("%w: owner/repo/head required", ErrUnavailable)
	}

	token, err := c.tokens.Token(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("%w: install token: %w", ErrUnavailable, err)
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/compare/%s...%s",
		GitHubAPI, owner, repo, base, head)

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		files, truncated, err := c.fetchOnce(ctx, endpoint, token)
		if err == nil {
			if truncated {
				return nil, ErrTruncated
			}
			return files, nil
		}
		lastErr = err
		// Retry only on 429 with a parseable Retry-After.
		if !isRetryable(err) || attempt == c.maxRetries {
			break
		}
		delay := retryAfterDelay(err, c.baseBackoff)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
		case <-time.After(delay):
		}
	}
	// 429 retry-exhausted is semantically equivalent to
	// truncation — caller can't trust the diff.
	if lastErr != nil && isRetryable(lastErr) {
		return nil, ErrTruncated
	}
	return nil, fmt.Errorf("%w: %w", ErrUnavailable, lastErr)
}

// fetchOnce does a single compare-API call. Returns
// (files, truncated, err) so the retry loop can distinguish
// "good response, but huge diff" (truncated=true) from
// "transport / 4xx / 5xx" (err != nil).
func (c *httpChangedFiles) fetchOnce(
	ctx context.Context,
	endpoint, token string,
) ([]string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()

	// 429: surface retry metadata via a typed error so the
	// caller can honor Retry-After.
	if resp.StatusCode == http.StatusTooManyRequests {
		ra := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, false, &retryableError{status: resp.StatusCode, retryAfter: ra, body: readErrorBody(resp)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("status=%d body=%s",
			resp.StatusCode, strings.TrimSpace(readErrorBody(resp)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, false, err
	}
	var payload compareResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false, fmt.Errorf("decode: %w", err)
	}

	// Truncation detection: commits paginated past page 1, or
	// files capped at 300. Either means the diff is too large to
	// trust per ADR-050 §109.
	if payload.TotalCommits > len(payload.Commits) || len(payload.Commits) >= compareCommitsCap {
		// total_commits > len(commits) — the canonical
		// signal that paginated past page 1.
		// len(commits) >= compareCommitsCap — defense in
		// depth at the documented per-page boundary.
		return nil, true, nil
	}
	if len(payload.Files) >= compareFilesCap {
		return nil, true, nil
	}

	files := make([]string, 0, len(payload.Files))
	for _, f := range payload.Files {
		if f.Filename != "" {
			files = append(files, f.Filename)
		}
		// Renames: also surface the previous path. A move from
		// outside a workload's RootDir → into it must rebuild
		// the destination workload (the new path is the source
		// of truth), and a move out doesn't need to rebuild
		// the source (the build would produce the same image).
		// Including the previous path is harmless for the
		// filter — workloads whose RootDir contains the
		// previous path will simply also be matched.
		if f.PreviousFilename != "" && f.PreviousFilename != f.Filename {
			files = append(files, f.PreviousFilename)
		}
	}
	return files, false, nil
}

// retryableError marks a 429 response so the retry loop can
// branch on it. The status + retryAfter fields drive the
// Retry-After-aware backoff.
type retryableError struct {
	status     int
	retryAfter time.Duration
	body       string
}

func (e *retryableError) Error() string {
	return fmt.Sprintf("githubd: compare: status=%d retry_after=%s body=%s",
		e.status, e.retryAfter, strings.TrimSpace(e.body))
}

func isRetryable(err error) bool {
	var re *retryableError
	return errors.As(err, &re)
}

// retryAfterDelay applies a ±10% jitter to the Retry-After
// value (parsed below) or falls back to baseBackoff with
// exponential growth on consecutive attempts.
func retryAfterDelay(err error, base time.Duration) time.Duration {
	var re *retryableError
	if errors.As(err, &re) && re.retryAfter > 0 {
		return jitter(re.retryAfter)
	}
	return jitter(base)
}

// jitter applies ±10% randomness to d to avoid stampede when
// many callers retry against the same window.
func jitter(d time.Duration) time.Duration {
	delta := float64(d) * 0.1
	offset := (rand.Float64()*2 - 1) * delta //nolint:gosec // jitter is non-crypto
	return d + time.Duration(offset)
}

// parseRetryAfter parses the Retry-After header (seconds, per
// GitHub's API; HTTP-date is also valid but uncommon for
// rate-limit responses). Returns 0 on any parse failure so
// retryAfterDelay falls back to the base backoff.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	// HTTP-date form: not implemented (GitHub uses delta-seconds).
	return 0
}

// readErrorBody drains the response body up to 1 KiB for error
// reporting. Bounded so a hostile upstream can't blow up
// memory through the error path. Named readErrorBody (not
// readBody) to avoid colliding with server.go's webhook
// body-reader helper.
func readErrorBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	return string(b)
}
