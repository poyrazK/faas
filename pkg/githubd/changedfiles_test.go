// changedfiles_test.go — tests for the GitHub compare-API client
// (ADR-050 §103-109). Pins the truncation detection, the
// retry-on-429 budget, and the standard outbound headers.
package githubd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fixedClient returns a TokenCache backed by a stub fetcher and a
// singleHostClient that routes requests to the supplied test
// server. The token returned by the stub is constant so the
// Authorization-header test can assert the bearer prefix verbatim.
func fixedClient(t *testing.T, srv *httptest.Server, token string) (ChangedFilesClient, *TokenCache) {
	t.Helper()
	fetcher := fakeFetcher(func(ctx context.Context, id int64) (string, time.Time, error) {
		return token, time.Now().Add(time.Hour), nil
	})
	tc := NewTokenCache(fetcher, time.Minute)
	hc := &singleHostClient{base: srv.Client(), api: srv.URL}
	return NewHTTPChangedFiles(tc, hc), tc
}

func TestChangedFiles_Basic(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"ahead",
			"ahead_by":1,
			"behind_by":0,
			"total_commits":1,
			"commits":[{"sha":"c1"}],
			"files":[
				{"filename":"services/auth/api/index.ts","status":"modified"},
				{"filename":"services/auth/api/Dockerfile","status":"modified"},
				{"filename":"README.md","status":"modified"}
			]
		}`))
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok")
	got, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "base-sha", "head-sha")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	want := []string{
		"services/auth/api/index.ts",
		"services/auth/api/Dockerfile",
		"README.md",
	}
	if !stringSliceEq(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestChangedFiles_RenamedFileIncludesBothPaths(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"status":"ahead","ahead_by":1,"behind_by":0,
			"total_commits":1,"commits":[{"sha":"c1"}],
			"files":[
				{"filename":"services/auth/api/x.ts","previous_filename":"services/auth/y.ts","status":"renamed"}
			]
		}`))
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok")
	got, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "base-sha", "head-sha")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	want := []string{"services/auth/api/x.ts", "services/auth/y.ts"}
	if !stringSliceEq(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

func TestChangedFiles_RemovedFileIncluded(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"status":"ahead","ahead_by":1,"behind_by":0,
			"total_commits":1,"commits":[{"sha":"c1"}],
			"files":[
				{"filename":"services/auth/api/deprecated.go","status":"removed"}
			]
		}`))
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok")
	got, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "base-sha", "head-sha")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(got) != 1 || got[0] != "services/auth/api/deprecated.go" {
		t.Errorf("files = %v, want [services/auth/api/deprecated.go]", got)
	}
}

func TestChangedFiles_TruncatedByCommitsPagination(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 250 total commits but only 2 in the page → diff is too
		// large to trust per ADR-050 §109.
		_, _ = w.Write([]byte(`{
			"status":"ahead","ahead_by":250,"behind_by":0,
			"total_commits":250,
			"commits":[{"sha":"c1"},{"sha":"c2"}],
			"files":[{"filename":"x","status":"modified"}]
		}`))
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok")
	_, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "base-sha", "head-sha")
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("err = %v, want ErrTruncated", err)
	}
}

func TestChangedFiles_TruncatedByFiles300Cap(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := []byte(`{"status":"ahead","ahead_by":1,"behind_by":0,"total_commits":1,"commits":[{"sha":"c1"}],"files":[`)
		for i := 0; i < compareFilesCap; i++ {
			if i > 0 {
				body = append(body, ',')
			}
			body = append(body, []byte(`{"filename":"f`+strconv.Itoa(i)+`","status":"modified"}`)...)
		}
		body = append(body, []byte(`]}`)...)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok")
	_, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "base-sha", "head-sha")
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("err = %v, want ErrTruncated", err)
	}
}

func TestChangedFiles_RetryOn429(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{
			"status":"ahead","ahead_by":1,"behind_by":0,
			"total_commits":1,"commits":[{"sha":"c1"}],
			"files":[{"filename":"a","status":"modified"}]
		}`))
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok")
	got, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "base-sha", "head-sha")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("files = %v, want [a]", got)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("calls = %d, want 2 (initial + 1 retry)", calls)
	}
}

func TestChangedFiles_RetryExhaustedMapsToTruncated(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok")
	_, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "base-sha", "head-sha")
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("err = %v, want ErrTruncated (429-exhausted is semantically truncation)", err)
	}
	// 1 initial + 2 retries = 3 attempts.
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestChangedFiles_404ReturnsUnavailable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok")
	_, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "base-sha", "head-sha")
	// 404 is mapped to ErrUnavailable (NOT ErrTruncated) so the
	// caller can distinguish a force-push wipe (full rebuild
	// triggered by "truncated" semantics) from a missing-repo
	// race or ACL change (where the caller may want to surface
	// a different audit row). Pinned by the M2 review fix.
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUnavailable)", err)
	}
	if errors.Is(err, ErrTruncated) {
		t.Errorf("err = ErrTruncated, want ErrUnavailable (404 must not be classified as truncation)")
	}
}

// TestRetryAfterDelay_HonorsParsedHeader asserts the retry-backoff
// helper derives the delay from the Retry-After header (delta-seconds
// form) instead of the base backoff. The M2 review flagged that the
// production client invokes retryAfterDelay with parseRetryAfter's
// output; this pins the contract without paying the multi-second
// cost of a real retry loop.
//
// Jitter is ±10% per retryAfterDelay, so the asserted floor is the
// parsed value minus 5% (round down) and the ceiling is the parsed
// value plus 10% (round up).
func TestRetryAfterDelay_HonorsParsedHeader(t *testing.T) {
	t.Parallel()
	const base = 200 * time.Millisecond
	// Happy path: Retry-After: 5 → delay ≈ 5s (±10% jitter).
	parsed := parseRetryAfter("5")
	if parsed != 5*time.Second {
		t.Fatalf("parseRetryAfter(\"5\") = %v, want 5s", parsed)
	}
	delay := retryAfterDelay(&retryableError{retryAfter: parsed}, base)
	if delay < 4500*time.Millisecond || delay > 5500*time.Millisecond {
		t.Errorf("retryAfterDelay(parsed=5s) = %v, want 4.5s..5.5s", delay)
	}
	// Empty header → parseRetryAfter returns 0 → retryAfterDelay
	// falls back to base. Jitter is ±10%, so the bound is the
	// base ± 10%. Pinned by the second case.
	delay = retryAfterDelay(&retryableError{retryAfter: 0}, base)
	if delay < base-base/10 || delay > base+base/10 {
		t.Errorf("retryAfterDelay(zero retryAfter) = %v, want %v±10%%", delay, base)
	}
	// Unparseable header → parseRetryAfter returns 0 → same fallback.
	// The zero-value path above exercises the same code path.
	if got := parseRetryAfter("not-a-number"); got != 0 {
		t.Errorf("parseRetryAfter(\"not-a-number\") = %v, want 0", got)
	}
}

func TestChangedFiles_StandardHeaders(t *testing.T) {
	t.Parallel()
	var seenAuth, seenAccept, seenVersion, seenUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenAccept = r.Header.Get("Accept")
		seenVersion = r.Header.Get("X-GitHub-Api-Version")
		seenUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"status":"ahead","ahead_by":0,"behind_by":0,"total_commits":0,"commits":[],"files":[]}`))
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok-xyz")
	_, _ = client.ChangedFiles(context.Background(), 7, "octo", "api", "b", "h")

	if seenAuth != "Bearer tok-xyz" {
		t.Errorf("Authorization = %q, want %q", seenAuth, "Bearer tok-xyz")
	}
	if seenAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q, want application/vnd.github+json", seenAccept)
	}
	if seenVersion != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version = %q, want 2022-11-28", seenVersion)
	}
	if seenUA != "faas-githubd/1.0" {
		t.Errorf("User-Agent = %q, want faas-githubd/1.0", seenUA)
	}
}

func TestChangedFiles_EmptyBaseTruncated(t *testing.T) {
	t.Parallel()
	// Empty base can't form a compare URL — map to truncation
	// so the caller falls back to full fan-out.
	client, _ := fixedClient(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called for empty base")
	})), "tok")
	_, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "", "h")
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("err = %v, want ErrTruncated", err)
	}
}

func TestChangedFiles_EmptyOwnerRepoUnavailable(t *testing.T) {
	t.Parallel()
	client, _ := fixedClient(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called for empty owner/repo")
	})), "tok")
	_, err := client.ChangedFiles(context.Background(), 7, "", "api", "b", "h")
	if err == nil || errors.Is(err, ErrTruncated) {
		t.Errorf("err = %v, want wrapped ErrUnavailable", err)
	}
}

func TestChangedFiles_TruncatedByCommitsBoundary(t *testing.T) {
	t.Parallel()
	// Pins review finding #2: a payload where total_commits ==
	// len(commits) == compareCommitsCap (250) is misclassified as
	// trustworthy under the old `TotalCommits > len(Commits)`
	// check. The new `>=` boundary mirrors the files-cap check
	// (defense in depth at the per-page boundary).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		commits := make([]string, 0, compareCommitsCap)
		for i := 0; i < compareCommitsCap; i++ {
			commits = append(commits, fmt.Sprintf(`{"sha":"c%d"}`, i))
		}
		body := fmt.Sprintf(`{"status":"ahead","ahead_by":250,"behind_by":0,"total_commits":250,"commits":[%s],"files":[{"filename":"x","status":"modified"}]}`,
			strings.Join(commits, ","))
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client, _ := fixedClient(t, srv, "tok")
	_, err := client.ChangedFiles(context.Background(), 7, "octo", "api", "base-sha", "head-sha")
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("err = %v, want ErrTruncated (250-commit boundary must trigger fallback)", err)
	}
}

// stringSliceEq compares two unordered []string values element
// by element. Order is significant (compare preserves order);
// duplicates are allowed.
func stringSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// scriptedChangedFiles is a ChangedFilesClient stub that
// returns (files, err) pairs from a scripted slice on each call.
// The breaker tests use this to drive consecutive-failure
// sequences without needing a live HTTP server.
type scriptedChangedFiles struct {
	steps []scriptStep
	calls int
}

type scriptStep struct {
	files []string
	err   error
}

func (s *scriptedChangedFiles) ChangedFiles(_ context.Context, _ int64, _, _, _, _ string) ([]string, error) {
	idx := s.calls
	s.calls++
	if idx >= len(s.steps) {
		// Default: repeat the last step (tests that expect a
		// tripped breaker rely on this — every subsequent call
		// short-circuits at the wrapper).
		idx = len(s.steps) - 1
	}
	return s.steps[idx].files, s.steps[idx].err
}

func TestBreaker_NotTripped_BelowThreshold(t *testing.T) {
	t.Parallel()
	// 2 consecutive failures (below threshold of 3) must NOT
	// trip the breaker. The 3rd call still goes through to
	// the inner client.
	inner := &scriptedChangedFiles{steps: []scriptStep{
		{files: nil, err: ErrUnavailable},
		{files: nil, err: ErrUnavailable},
		{files: []string{"a/b/c.ts"}, err: nil},
	}}
	clock := time.Now
	w := NewBreakerChangedFiles(inner, clock)
	for i := 0; i < 3; i++ {
		files, err := w.ChangedFiles(context.Background(), 1, "octo", "api", "base", "head")
		if i < 2 && !errors.Is(err, ErrUnavailable) {
			t.Fatalf("call %d: err = %v, want ErrUnavailable", i, err)
		}
		if i == 2 && err != nil {
			t.Fatalf("call %d: err = %v, want nil", i, err)
		}
		if i == 2 && !stringSliceEq(files, []string{"a/b/c.ts"}) {
			t.Fatalf("call %d: files = %v, want [a/b/c.ts]", i, files)
		}
	}
	if inner.calls != 3 {
		t.Errorf("inner.calls = %d, want 3 (breaker must not short-circuit below threshold)", inner.calls)
	}
}

func TestBreaker_TripsAfterThreshold(t *testing.T) {
	t.Parallel()
	inner := &scriptedChangedFiles{steps: []scriptStep{
		{files: nil, err: ErrUnavailable},
		{files: nil, err: ErrUnavailable},
		{files: nil, err: ErrUnavailable}, // 3rd → trips
		{files: []string{"should-not-run.ts"}, err: nil},
	}}
	w := NewBreakerChangedFiles(inner, time.Now)
	// First three calls land ErrUnavailable on the inner.
	for i := 0; i < 3; i++ {
		_, err := w.ChangedFiles(context.Background(), 1, "octo", "api", "base", "head")
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("call %d: err = %v, want ErrUnavailable", i, err)
		}
	}
	// 4th call must short-circuit at the breaker — inner
	// must NOT be called.
	_, err := w.ChangedFiles(context.Background(), 1, "octo", "api", "base", "head")
	if !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("post-trip err = %v, want ErrBreakerOpen", err)
	}
	if inner.calls != 3 {
		t.Errorf("inner.calls = %d, want 3 (4th call must short-circuit)", inner.calls)
	}
}

func TestBreaker_TruncationDoesNotTrip(t *testing.T) {
	t.Parallel()
	// ErrTruncated is a "diff too big" signal, not an outage.
	// Counting it toward the breaker would trip on every push
	// to a busy monorepo.
	inner := &scriptedChangedFiles{steps: []scriptStep{
		{files: nil, err: ErrTruncated},
		{files: nil, err: ErrTruncated},
		{files: nil, err: ErrTruncated},
		{files: nil, err: ErrTruncated},
	}}
	w := NewBreakerChangedFiles(inner, time.Now)
	for i := 0; i < 4; i++ {
		_, err := w.ChangedFiles(context.Background(), 1, "octo", "api", "base", "head")
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("call %d: err = %v, want ErrTruncated", i, err)
		}
	}
	if inner.calls != 4 {
		t.Errorf("inner.calls = %d, want 4 (truncation must NOT trip breaker)", inner.calls)
	}
}

func TestBreaker_ResetsAfterCooldown(t *testing.T) {
	t.Parallel()
	inner := &scriptedChangedFiles{steps: []scriptStep{
		{files: nil, err: ErrUnavailable},
		{files: nil, err: ErrUnavailable},
		{files: nil, err: ErrUnavailable}, // trip
		{files: []string{"post-cooldown.ts"}, err: nil},
	}}
	// Fake clock advances exactly past the cooldown.
	var now time.Time
	clock := func() time.Time { return now }
	w := NewBreakerChangedFiles(inner, clock)
	for i := 0; i < 3; i++ {
		_, _ = w.ChangedFiles(context.Background(), 1, "octo", "api", "base", "head")
	}
	// 4th call inside the cooldown window → ErrBreakerOpen.
	_, err := w.ChangedFiles(context.Background(), 1, "octo", "api", "base", "head")
	if !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("inside-cooldown err = %v, want ErrBreakerOpen", err)
	}
	// Advance the clock past the cooldown.
	now = now.Add(breakerCooldown + time.Second)
	// 5th call goes through to inner (which serves the 4th
	// step — post-cooldown.ts).
	files, err := w.ChangedFiles(context.Background(), 1, "octo", "api", "base", "head")
	if err != nil {
		t.Fatalf("post-cooldown err = %v, want nil", err)
	}
	if !stringSliceEq(files, []string{"post-cooldown.ts"}) {
		t.Errorf("post-cooldown files = %v, want [post-cooldown.ts]", files)
	}
	if inner.calls != 4 {
		t.Errorf("inner.calls = %d, want 4 (post-cooldown must call inner)", inner.calls)
	}
}

func TestBreaker_SuccessResetsFailureCount(t *testing.T) {
	t.Parallel()
	inner := &scriptedChangedFiles{steps: []scriptStep{
		{files: nil, err: ErrUnavailable},
		{files: nil, err: ErrUnavailable},
		{files: []string{"ok.ts"}, err: nil}, // success resets counter
		{files: nil, err: ErrUnavailable},    // counter starts at 1
		{files: nil, err: ErrUnavailable},    // counter at 2
		{files: []string{"ok2.ts"}, err: nil},
	}}
	w := NewBreakerChangedFiles(inner, time.Now)
	for i := 0; i < 6; i++ {
		_, _ = w.ChangedFiles(context.Background(), 1, "octo", "api", "base", "head")
	}
	if inner.calls != 6 {
		t.Errorf("inner.calls = %d, want 6 (successes must reset counter, no false trip)", inner.calls)
	}
}
