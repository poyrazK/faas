package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/state"
)

type sourceRefTestInstalls struct {
	inst state.GitHubInstall
	err  error
}

func (f sourceRefTestInstalls) ForAccount(context.Context, string) (state.GitHubInstall, error) {
	if f.err != nil {
		return state.GitHubInstall{}, f.err
	}
	return f.inst, nil
}

type sourceRefTestTokens struct {
	mu          sync.Mutex
	calls       int
	invalidates int
}

func (f *sourceRefTestTokens) Token(context.Context, int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return "token-" + strconv.Itoa(f.calls), nil
}

func (f *sourceRefTestTokens) Invalidate(int64) {
	f.mu.Lock()
	f.invalidates++
	f.mu.Unlock()
}

func TestSourceRefStreamer_ResolvesBranchAndPinsArchive(t *testing.T) {
	const accountID = "acct-1"
	const installID = int64(42)
	commitSHA := strings.Repeat("a", 40)
	archive := []byte("tarball-bytes")
	var commitCalls, archiveCalls int
	var archivePath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-1" {
			t.Errorf("authorization = %q, want token-1", r.Header.Get("Authorization"))
		}
		switch {
		case strings.Contains(r.URL.Path, "/commits/"):
			commitCalls++
			if !strings.HasSuffix(r.URL.EscapedPath(), "commits/feature%2Fbranch") &&
				!strings.HasSuffix(r.URL.Path, "commits/feature/branch") {
				t.Errorf("commit lookup path = %q, want feature/branch", r.URL.EscapedPath())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"sha":"`+commitSHA+`"}`)
		case strings.Contains(r.URL.Path, "/tar.gz/"):
			archiveCalls++
			archivePath = r.URL.Path
			if !strings.HasSuffix(r.URL.Path, "/tar.gz/"+commitSHA) {
				t.Errorf("archive path = %q, want canonical SHA", r.URL.Path)
			}
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tokens := &sourceRefTestTokens{}
	streamer := newSourceRefStreamer(
		sourceRefTestInstalls{inst: state.GitHubInstall{
			AccountID: accountID, InstallationID: installID,
		}},
		tokens,
		srv.Client(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	streamer.apiBaseURL = srv.URL
	streamer.codeloadBaseURL = srv.URL

	res, err := streamer.Stream(context.Background(), accountID, installID, "acme/api", "feature/branch", 0)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if string(got) != string(archive) {
		t.Errorf("archive = %q, want %q", got, archive)
	}
	if res.ResolvedCommitSHA != commitSHA {
		t.Errorf("resolved SHA = %q, want %q", res.ResolvedCommitSHA, commitSHA)
	}
	if commitCalls != 1 || archiveCalls != 1 {
		t.Errorf("commit calls = %d, archive calls = %d, want 1 each", commitCalls, archiveCalls)
	}
	if archivePath == "" {
		t.Fatal("archive endpoint was not called")
	}
}

func TestSourceRefStreamer_RetriesUnauthorizedCommitLookup(t *testing.T) {
	commitSHA := strings.Repeat("b", 40)
	var commitCalls, archiveCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/commits/") {
			commitCalls++
			if commitCalls == 1 {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"sha":"`+commitSHA+`"}`)
			return
		}
		if strings.Contains(r.URL.Path, "/tar.gz/") {
			archiveCalls++
			_, _ = io.WriteString(w, "ok")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	tokens := &sourceRefTestTokens{}
	streamer := newSourceRefStreamer(
		sourceRefTestInstalls{inst: state.GitHubInstall{
			AccountID: "acct", InstallationID: 7,
		}},
		tokens,
		srv.Client(),
		nil,
	)
	streamer.apiBaseURL = srv.URL
	streamer.codeloadBaseURL = srv.URL

	res, err := streamer.Stream(context.Background(), "acct", 7, "acme/api", "main", 0)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if commitCalls != 2 || archiveCalls != 1 {
		t.Errorf("commit calls = %d, archive calls = %d, want 2 and 1", commitCalls, archiveCalls)
	}
	tokens.mu.Lock()
	gotCalls, gotInvalidates := tokens.calls, tokens.invalidates
	tokens.mu.Unlock()
	if gotCalls != 2 || gotInvalidates != 1 {
		t.Errorf("token calls = %d invalidates = %d, want 2 and 1", gotCalls, gotInvalidates)
	}
}

func TestIsValidSourceRefRef(t *testing.T) {
	for _, tc := range []struct {
		ref string
		ok  bool
	}{
		{"main", true},
		{"release/2026-q3", true},
		{strings.Repeat("a", 40), true},
		{"abc", false},
		{"main/../../etc", false},
		{"feature//x", false},
		{".hidden", false},
		{"release.lock", false},
		{"release.", false},
		{"release./candidate", false},
		{"feature*", false},
		{"@", false},
		{"main^", false},
	} {
		if got := isValidSourceRefRef(tc.ref); got != tc.ok {
			t.Errorf("isValidSourceRefRef(%q) = %v, want %v", tc.ref, got, tc.ok)
		}
	}
}

func TestSourceRefStreamer_InstallMismatchFailsClosed(t *testing.T) {
	streamer := newSourceRefStreamer(
		sourceRefTestInstalls{inst: state.GitHubInstall{
			AccountID: "acct", InstallationID: 9,
		}},
		&sourceRefTestTokens{},
		http.DefaultClient,
		nil,
	)
	_, err := streamer.Stream(context.Background(), "acct", 8, "acme/api", "main", 0)
	if !errors.Is(err, githubd.ErrNoBinding) {
		t.Fatalf("Stream error = %v, want ErrNoBinding", err)
	}
}

func TestSourceRefStreamer_InstallAccountMismatchFailsClosed(t *testing.T) {
	// The lookup seam is deliberately allowed to return a wrong-account
	// row here; the production adapter filters by account, but this guard
	// keeps a future adapter bug from turning into a token-use primitive.
	tokens := &sourceRefTestTokens{}
	streamer := newSourceRefStreamer(
		sourceRefTestInstalls{inst: state.GitHubInstall{
			AccountID: "other-account", InstallationID: 9,
		}},
		tokens,
		http.DefaultClient,
		nil,
	)
	_, err := streamer.Stream(context.Background(), "acct", 9, "acme/api", "main", 0)
	if !errors.Is(err, githubd.ErrNoBinding) {
		t.Fatalf("Stream error = %v, want ErrNoBinding", err)
	}
	tokens.mu.Lock()
	calls := tokens.calls
	tokens.mu.Unlock()
	if calls != 0 {
		t.Fatalf("token calls = %d, want 0 on account mismatch", calls)
	}
}
