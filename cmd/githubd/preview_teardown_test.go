package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitHubDeploymentEnvironment(t *testing.T) {
	cases := []struct {
		kind, scope, slug, previewOf, want string
	}{
		{kind: string(state.DeploymentKindGitHub), slug: "api", want: "production/api"},
		{kind: string(state.DeploymentKindGitHub), scope: "staging", slug: "api", want: "staging/api"},
		{kind: string(state.DeploymentKindPreview), slug: "pr-42-api", previewOf: "api", want: "preview/api"},
		{kind: string(state.DeploymentKindPreview), slug: "pr-43-api", previewOf: "api", want: "preview/api"},
		{kind: string(state.DeploymentKindPreview), slug: "pr-42-legacy", want: "preview/pr-42-legacy"},
	}
	for _, tc := range cases {
		if got := githubDeploymentEnvironment(tc.kind, tc.scope, tc.slug, tc.previewOf); got != tc.want {
			t.Errorf("githubDeploymentEnvironment(%q, %q, %q, %q) = %q, want %q", tc.kind, tc.scope, tc.slug, tc.previewOf, got, tc.want)
		}
	}
}

type teardownFetcher struct{}

func (teardownFetcher) ExchangeInstallationToken(context.Context, int64) (string, time.Time, error) {
	return "ghs_token", time.Now().Add(time.Hour), nil
}

type teardownDeployments struct{ ids map[string]int64 }

func (d teardownDeployments) GitHubDeploymentID(_ context.Context, id string) (int64, error) {
	if githubID, ok := d.ids[id]; ok {
		return githubID, nil
	}
	return 0, githubd.ErrGitHubDeploymentNotFound
}

func (d teardownDeployments) SaveGitHubDeploymentID(context.Context, string, int64) error { return nil }

type teardownHTTP struct {
	comments string
	calls    []string
	bodies   []string
}

func (h *teardownHTTP) Do(req *http.Request) (*http.Response, error) {
	h.calls = append(h.calls, req.Method+" "+req.URL.Path)
	body := []byte{}
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	h.bodies = append(h.bodies, string(body))
	status, payload := http.StatusOK, `{}`
	switch {
	case req.Method == http.MethodGet:
		payload = h.comments
	case req.Method == http.MethodPost:
		status = http.StatusCreated
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewBufferString(payload)), Header: http.Header{}}, nil
}

func TestSyncTornDownPreview(t *testing.T) {
	marker := "<!-- gregale-preview:pr-42-api -->"
	comments, _ := json.Marshal([]map[string]any{{"id": 31, "body": marker + "\nlive", "user": map[string]any{"type": "Bot"}}})
	h := &teardownHTTP{comments: string(comments)}
	checks := &githubd.ChecksAPI{Tokens: githubd.NewTokenCache(teardownFetcher{}, time.Minute), HTTP: h,
		Deployments: teardownDeployments{ids: map[string]int64{"dep-root": 900}}}
	preview := tornDownPreview{deploymentID: "dep-root", installationID: 7, repo: "octo/api", commitSHA: strings.Repeat("a", 40),
		environment: "preview/api", appSlug: "pr-42-api", prNumber: 42}
	if err := syncTornDownPreview(context.Background(), checks, preview); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /repos/octo/api/deployments/900/statuses", "GET /repos/octo/api/issues/42/comments", "PATCH /repos/octo/api/issues/comments/31"}
	if strings.Join(h.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %v, want %v", h.calls, want)
	}
	if !strings.Contains(h.bodies[0], `"state":"inactive"`) || !strings.Contains(h.bodies[2], "torn down") {
		t.Fatalf("bodies = %v, want an inactive status and a torn-down comment", h.bodies)
	}

	// A dependency preview that never reached GitHub posts nothing.
	h = &teardownHTTP{comments: `[]`}
	checks.HTTP = h
	preview.deploymentID, preview.appSlug = "dep-worker", "pr-42-worker"
	if err := syncTornDownPreview(context.Background(), checks, preview); err != nil {
		t.Fatal(err)
	}
	if len(h.calls) != 1 || !strings.HasPrefix(h.calls[0], "GET ") {
		t.Fatalf("calls = %v, want only the comment lookup", h.calls)
	}
}
