// adr: 20261126164847
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/gitfetch"
	"github.com/onebox-faas/faas/pkg/githubd"
)

const publicTestSHA = "89abcdef0123456789abcdef0123456789abcdef"

// publicGitHub fakes the three upstream requests: repository, commit and
// codeload archive. repoStatus/repoBody answer the repository lookup.
type publicGitHub struct {
	repoStatus int
	repoBody   string
	archive    string

	mu       sync.Mutex
	paths    []string
	authHdrs []string
}

func (g *publicGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.paths = append(g.paths, r.URL.Path)
	g.authHdrs = append(g.authHdrs, r.Header.Get("Authorization"))
	g.mu.Unlock()
	switch {
	case r.URL.Path == "/repos/gregale-dev/starter":
		w.WriteHeader(g.repoStatus)
		_, _ = io.WriteString(w, g.repoBody)
	case r.URL.Path == "/repos/gregale-dev/starter/commits/main":
		_, _ = io.WriteString(w, publicTestSHA)
	case r.URL.Path == "/gregale-dev/starter/tar.gz/"+publicTestSHA:
		_, _ = io.WriteString(w, g.archive)
	default:
		http.NotFound(w, r)
	}
}

func (g *publicGitHub) requests() ([]string, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.paths...), append([]string(nil), g.authHdrs...)
}

func newPublicTestStreamer(t *testing.T, token string, upstream *publicGitHub) *publicSourceRefStreamer {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	p := newPublicSourceRefStreamer(token, srv.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.archive.apiBaseURL, p.archive.codeloadBaseURL = srv.URL, srv.URL
	return p
}

const publicRepoJSON = `{"full_name":"gregale-dev/starter","private":false,"visibility":"public"}`

func TestPublicSourceRefStreamer_PublicRepoPinsCommit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		token    string
		wantAuth string
	}{
		{name: "anonymous", token: "", wantAuth: ""},
		{name: "public-only token", token: " github_pat_public \n", wantAuth: "Bearer github_pat_public"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &publicGitHub{repoStatus: http.StatusOK, repoBody: publicRepoJSON, archive: "tar.gz bytes"}
			p := newPublicTestStreamer(t, tc.token, upstream)

			res, err := p.StreamPublic(context.Background(), "gregale-dev/starter", "main", 1<<20)
			if err != nil {
				t.Fatalf("StreamPublic: %v", err)
			}
			body, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if string(body) != "tar.gz bytes" || res.ResolvedCommitSHA != publicTestSHA {
				t.Fatalf("stream = %q at %q, want archive at %s", body, res.ResolvedCommitSHA, publicTestSHA)
			}
			paths, auths := upstream.requests()
			wantPaths := []string{"/repos/gregale-dev/starter", "/repos/gregale-dev/starter/commits/main", "/gregale-dev/starter/tar.gz/" + publicTestSHA}
			if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
				t.Fatalf("requests = %v, want %v", paths, wantPaths)
			}
			for i, got := range auths {
				if got != tc.wantAuth {
					t.Fatalf("request %d Authorization = %q, want %q", i, got, tc.wantAuth)
				}
			}
		})
	}
}

func TestPublicSourceRefStreamer_RefusesWhatGitHubDoesNotConfirmPublic(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "private", status: http.StatusOK, body: `{"full_name":"gregale-dev/starter","private":true,"visibility":"private"}`, want: githubd.ErrRepositoryNotPublic},
		{name: "internal visibility", status: http.StatusOK, body: `{"full_name":"gregale-dev/starter","private":false,"visibility":"internal"}`, want: githubd.ErrRepositoryNotPublic},
		{name: "private field missing", status: http.StatusOK, body: `{"full_name":"gregale-dev/starter"}`, want: githubd.ErrRepositoryNotPublic},
		{name: "renamed", status: http.StatusOK, body: `{"full_name":"gregale-dev/starter-v2","private":false}`, want: githubd.ErrRepositoryNotPublic},
		{name: "missing or hidden", status: http.StatusNotFound, body: `{}`, want: githubd.ErrRepositoryNotPublic},
		{name: "rate limited", status: http.StatusForbidden, body: `{}`, want: gitfetch.ErrUnauthorized},
		{name: "too many requests", status: http.StatusTooManyRequests, body: `{}`, want: gitfetch.ErrUnauthorized},
		{name: "token rejected", status: http.StatusUnauthorized, body: `{}`, want: gitfetch.ErrUnauthorized},
		{name: "upstream failure", status: http.StatusBadGateway, body: `{}`, want: gitfetch.ErrBadArchive},
		{name: "malformed body", status: http.StatusOK, body: `{`, want: gitfetch.ErrBadArchive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &publicGitHub{repoStatus: tc.status, repoBody: tc.body, archive: "secret source"}
			p := newPublicTestStreamer(t, "", upstream)

			_, err := p.StreamPublic(context.Background(), "gregale-dev/starter", "main", 1<<20)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if paths, _ := upstream.requests(); len(paths) != 1 {
				t.Fatalf("requests = %v; nothing past the repository lookup may run", paths)
			}
		})
	}
}

func TestPublicSourceRefStreamer_RejectsMalformedInputOffline(t *testing.T) {
	upstream := &publicGitHub{repoStatus: http.StatusOK, repoBody: publicRepoJSON}
	p := newPublicTestStreamer(t, "", upstream)
	for _, tc := range []struct{ repo, ref string }{
		{repo: "gregale-dev/starter/extra", ref: "main"},
		{repo: "../etc", ref: "main"},
		{repo: "gregale-dev/starter", ref: "../main"},
		{repo: "gregale-dev/starter", ref: "main?x=1"},
	} {
		if _, err := p.StreamPublic(context.Background(), tc.repo, tc.ref, 1<<20); !errors.Is(err, gitfetch.ErrBadArchive) {
			t.Fatalf("StreamPublic(%q, %q) = %v, want ErrBadArchive", tc.repo, tc.ref, err)
		}
	}
	if paths, _ := upstream.requests(); len(paths) != 0 {
		t.Fatalf("requests = %v, want none", paths)
	}
}

func TestPublicSourceRefStreamer_CapsArchive(t *testing.T) {
	upstream := &publicGitHub{repoStatus: http.StatusOK, repoBody: publicRepoJSON, archive: strings.Repeat("x", 64)}
	p := newPublicTestStreamer(t, "", upstream)

	res, err := p.StreamPublic(context.Background(), "gregale-dev/starter", "main", 10)
	if err != nil {
		t.Fatalf("StreamPublic: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	// The cap reader yields one byte past the limit so the caller can see
	// that the archive is too large.
	if len(body) != 11 {
		t.Fatalf("read %d bytes, want 11", len(body))
	}
}

func TestIsValidSourceRefRepoRejectsDotSegments(t *testing.T) {
	for repo, want := range map[string]bool{
		"gregale-dev/mcp-server-node": true,
		"a.b/c_d-e":                   true,
		"owner/.github":               true,
		"../etc":                      false,
		"owner/..":                    false,
		"./repo":                      false,
		"owner/.":                     false,
	} {
		if got := isValidSourceRefRepo(repo); got != want {
			t.Errorf("isValidSourceRefRepo(%q) = %v, want %v", repo, got, want)
		}
	}
}
