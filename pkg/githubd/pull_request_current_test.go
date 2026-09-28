package githubd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type stubCurrentPullRequests struct {
	snapshot PullRequestSnapshot
	err      error
}

func (s stubCurrentPullRequests) CurrentPullRequest(context.Context, int64, string, int) (PullRequestSnapshot, error) {
	return s.snapshot, s.err
}

func TestHTTPCurrentPullRequests(t *testing.T) {
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/repos/octo/api/pulls/42" ||
			r.Header.Get("Authorization") != "Bearer installation-token" {
			t.Errorf("current PR request = %s, auth = %q", r.URL.EscapedPath(), r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"number":42,"state":"open","head":{"sha":"`+sha+`","repo":{"full_name":"octo/api"}}}`)
	}))
	defer srv.Close()
	tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
		return "installation-token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	client := NewHTTPCurrentPullRequests(tokens, &singleHostClient{base: srv.Client(), api: srv.URL})
	got, err := client.CurrentPullRequest(context.Background(), 42, "octo/api", 42)
	if err != nil || got.State != "open" || got.HeadSHA != sha || got.HeadRepoFullName != "octo/api" {
		t.Fatalf("current PR = (%+v, %v)", got, err)
	}
}

func TestHTTPCurrentPullRequestsFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"upstream failure", http.StatusServiceUnavailable, `{}`},
		{"wrong number", http.StatusOK, `{"number":43,"state":"closed"}`},
		{"missing head", http.StatusOK, `{"number":42,"state":"open"}`},
		{"invalid response", http.StatusOK, `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
				return "installation-token", time.Now().Add(time.Hour), nil
			}), time.Minute)
			client := NewHTTPCurrentPullRequests(tokens, &singleHostClient{base: srv.Client(), api: srv.URL})
			if _, err := client.CurrentPullRequest(context.Background(), 42, "octo/api", 42); !errors.Is(err, ErrPullRequestUnavailable) {
				t.Fatalf("current PR error = %v, want unavailable", err)
			}
		})
	}
}

func TestHandlePullRequestIgnoresSupersededHeadAndClose(t *testing.T) {
	const oldSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const newSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)
	svc.PullRequests = stubCurrentPullRequests{snapshot: PullRequestSnapshot{
		State: "open", HeadSHA: newSHA, HeadRepoFullName: "octo/api",
	}}
	result, err := svc.handlePullRequest(context.Background(), pullRequestOpenedBody(42, newSHA))
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("current PR open = (%+v, %v)", result, err)
	}
	for _, body := range [][]byte{pullRequestSyncBody(42, oldSHA), pullRequestClosedBody(42, oldSHA)} {
		result, err := svc.handlePullRequest(context.Background(), body)
		if !errors.Is(err, ErrIgnored) || !result.WasIgnored {
			t.Fatalf("stale PR delivery = (%+v, %v), want ignored", result, err)
		}
	}
	svc.PullRequests = stubCurrentPullRequests{snapshot: PullRequestSnapshot{
		State: "open", HeadSHA: newSHA, HeadRepoFullName: "other/api",
	}}
	if result, err := svc.handlePullRequest(context.Background(), pullRequestSyncBody(42, newSHA)); !errors.Is(err, ErrIgnored) || !result.WasIgnored {
		t.Fatalf("current fork head = (%+v, %v), want ignored", result, err)
	}
	preview, err := rig.mem.AppBySlug(context.Background(), "pr-42-demo-app")
	if err != nil || preview.PreviewPrState != state.PreviewPrStateOpen {
		t.Fatalf("preview after stale events = (%+v, %v), want open", preview, err)
	}
	if len(rec.checks) != 1 || !strings.EqualFold(rec.checks[0].sha, newSHA) {
		t.Fatalf("stale events changed preview checks: %+v", rec.checks)
	}
}

func TestHandlePullRequestRetriesCurrentStateFailure(t *testing.T) {
	rig := newPreviewRig(t)
	svc, _ := newPreviewService(t, rig)
	svc.PullRequests = stubCurrentPullRequests{err: ErrPullRequestUnavailable}
	_, err := svc.handlePullRequest(context.Background(), pullRequestOpenedBody(42,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if !errors.Is(err, ErrPullRequestUnavailable) {
		t.Fatalf("current PR lookup error = %v, want retryable error", err)
	}
	if _, err := rig.mem.AppBySlug(context.Background(), "pr-42-demo-app"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("preview created before current PR check: %v", err)
	}
}
