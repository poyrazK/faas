// Checks API writer tests (slice 8, ADR-012).
package githubd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

func TestChecksAPI_PhaseMapping(t *testing.T) {
	cases := []struct {
		phase      githubdgrpc.CheckPhase
		wantStatus string
		wantConcl  string
		wantTitle  string
	}{
		{githubdgrpc.CheckPhaseQueued, "queued", "", "Build queued"},
		{githubdgrpc.CheckPhaseBuilding, "in_progress", "", "Build in progress"},
		{githubdgrpc.CheckPhaseLive, "completed", "success", "Deployment live"},
		{githubdgrpc.CheckPhaseFailed, "completed", "failure", "Deployment failed"},
	}
	for _, c := range cases {
		if got := phaseToStatus(c.phase); got != c.wantStatus {
			t.Errorf("phase %v: status = %q, want %q", c.phase, got, c.wantStatus)
		}
		if got := phaseToConclusion(c.phase); got != c.wantConcl {
			t.Errorf("phase %v: conclusion = %q, want %q", c.phase, got, c.wantConcl)
		}
		if got := phaseTitle(c.phase); got != c.wantTitle {
			t.Errorf("phase %v: title = %q, want %q", c.phase, got, c.wantTitle)
		}
	}
}

type fakeGitHubDeploymentStore struct {
	id    int64
	saves int
}

func (f *fakeGitHubDeploymentStore) GitHubDeploymentID(_ context.Context, _ string) (int64, error) {
	if f.id == 0 {
		return 0, ErrGitHubDeploymentNotFound
	}
	return f.id, nil
}

func (f *fakeGitHubDeploymentStore) SaveGitHubDeploymentID(_ context.Context, _ string, id int64) error {
	f.id = id
	f.saves++
	return nil
}

func TestChecksAPI_WriteGitHubDeploymentStatus_IsStableAcrossRetries(t *testing.T) {
	var paths []string
	var createBody map[string]any
	var statusBodies []map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/deployments") {
			_ = json.Unmarshal(body, &createBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":4242,"environment":"preview/demo"}`))
			return
		}
		var statusBody map[string]any
		_ = json.Unmarshal(body, &statusBody)
		statusBodies = append(statusBodies, statusBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":5252}`))
	}))
	defer fake.Close()

	store := &fakeGitHubDeploymentStore{}
	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, _ int64) (string, time.Time, error) {
		return "ghs_deploy_token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	c, err := NewChecksAPI(tokens, &singleHostClient{base: fake.Client(), api: fake.URL}, &fakeBindings{id: 99})
	if err != nil {
		t.Fatal(err)
	}
	c.WithGitHubDeploymentStore(store)
	update := GitHubDeploymentUpdate{
		LocalDeploymentID:     "deployment-1",
		InstallationID:        99,
		RepoFullName:          "octo/api",
		CommitSHA:             "deadbeef",
		Ref:                   "deadbeef",
		Environment:           "preview/demo",
		Status:                "building",
		Description:           "Gregale deployment deployment-1 for demo",
		TargetURL:             "https://gregale.dev/deployments/deployment-1",
		EnvironmentURL:        "https://demo.gregale.dev",
		LogURL:                "https://gregale.dev/logs/deployment-1",
		TransientEnvironment:  true,
		ProductionEnvironment: false,
	}
	if err := c.WriteGitHubDeploymentStatus(context.Background(), update); err != nil {
		t.Fatalf("first deployment status: %v", err)
	}
	if store.id != 4242 || store.saves != 1 {
		t.Fatalf("store = id %d saves %d, want id 4242 and one save", store.id, store.saves)
	}
	if len(paths) != 3 || paths[0] != "GET /repos/octo/api/deployments" ||
		paths[1] != "POST /repos/octo/api/deployments" ||
		paths[2] != "POST /repos/octo/api/deployments/4242/statuses" {
		t.Fatalf("request paths = %#v", paths)
	}
	if createBody["ref"] != "deadbeef" || createBody["environment"] != "preview/demo" || createBody["transient_environment"] != true {
		t.Fatalf("create body = %#v", createBody)
	}
	if !strings.Contains(createBody["description"].(string), "gregale-deployment:deployment-1") {
		t.Fatalf("create description missing stable marker: %#v", createBody["description"])
	}
	if len(statusBodies) != 1 || statusBodies[0]["state"] != "in_progress" {
		t.Fatalf("status bodies = %#v", statusBodies)
	}

	update.Status = "live"
	if err := c.WriteGitHubDeploymentStatus(context.Background(), update); err != nil {
		t.Fatalf("retry deployment status: %v", err)
	}
	if len(paths) != 4 || paths[3] != "POST /repos/octo/api/deployments/4242/statuses" {
		t.Fatalf("retry request paths = %#v", paths)
	}
	if len(statusBodies) != 2 || statusBodies[1]["state"] != "success" {
		t.Fatalf("retry status bodies = %#v", statusBodies)
	}
}

func TestGitHubDeploymentStateMapping(t *testing.T) {
	cases := map[string]string{
		"pending": "queued", "building": "in_progress", "imaging": "in_progress",
		"snapshotting": "in_progress", "live": "success", "failed": "failure",
		"cancelled": "inactive", "superseded": "inactive",
	}
	for status, want := range cases {
		got, ok := githubDeploymentState(status)
		if !ok || got != want {
			t.Errorf("githubDeploymentState(%q) = %q, %v; want %q, true", status, got, ok, want)
		}
	}
	if got, ok := githubDeploymentState("deleted"); ok || got != "" {
		t.Errorf("unknown state = %q, %v; want empty, false", got, ok)
	}
}

// TestChecksAPI_PreviewPhaseTitle pins the preview-specific
// title copy (issue #272 / ADR-094). The PR UI uses the
// Check Run title to render the row — keeping "Preview X"
// distinct from the production "Build X" copy lets operators
// distinguish the two pipelines on a busy PR.
func TestChecksAPI_PreviewPhaseTitle(t *testing.T) {
	cases := []struct {
		phase githubdgrpc.CheckPhase
		want  string
	}{
		{githubdgrpc.CheckPhaseQueued, "Preview queued"},
		{githubdgrpc.CheckPhaseBuilding, "Preview building"},
		{githubdgrpc.CheckPhaseLive, "Preview live"},
		{githubdgrpc.CheckPhaseFailed, "Preview failed"},
		{githubdgrpc.CheckPhaseUnspecified, "Preview"},
	}
	for _, c := range cases {
		if got := previewPhaseTitle(c.phase); got != c.want {
			t.Errorf("phase %v: title = %q, want %q", c.phase, got, c.want)
		}
	}
}

func TestChecksAPI_WriteCheck_HTTP(t *testing.T) {
	var hits atomic.Int32
	var gotBody map[string]any
	var gotAuth string
	var gotPath string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":12345}`))
	}))
	defer fake.Close()

	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, _ int64) (string, time.Time, error) {
		return "ghs_test_token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	c, err := NewChecksAPI(tokens, &singleHostClient{base: fake.Client(), api: fake.URL}, &fakeBindings{id: 42})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteCheck(context.Background(), "octo/api", "deadbeef", githubdgrpc.CheckPhaseQueued, "https://example.test/logs", "queued"); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
	if !strings.Contains(gotPath, "/repos/octo/api/check-runs") {
		t.Errorf("path = %q, want /repos/octo/api/check-runs", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("auth = %q, want Bearer prefix", gotAuth)
	}
	if gotBody["head_sha"] != "deadbeef" {
		t.Errorf("head_sha = %v, want deadbeef", gotBody["head_sha"])
	}
	if gotBody["status"] != "queued" {
		t.Errorf("status = %v, want queued", gotBody["status"])
	}
}

func TestChecksAPI_RejectsMissingArgs(t *testing.T) {
	c := &ChecksAPI{HTTP: http.DefaultClient}
	if err := c.WriteCheck(context.Background(), "", "sha", githubdgrpc.CheckPhaseQueued, "", ""); err == nil {
		t.Error("empty repo should error")
	}
	if err := c.WriteCheck(context.Background(), "owner/repo", "", githubdgrpc.CheckPhaseQueued, "", ""); err == nil {
		t.Error("empty sha should error")
	}
}

// _ keeps imports stable for future slices that add HTTPClient mocks.
var _ HTTPClient = (*http.Client)(nil)

// fakeBindings is the test stub for BindingsLookup. Returns a fixed
// install id by default; tests that need to simulate "no app
// bound" can construct it with id=0 (tokensForRepo will fail at
// the token-cache step).
type fakeBindings struct {
	id      int64
	err     error
	hits    atomic.Int32
	gotRepo string
}

func (f *fakeBindings) InstallationIDForRepo(_ context.Context, repoFullName string) (int64, error) {
	f.hits.Add(1)
	f.gotRepo = repoFullName
	return f.id, f.err
}

// TestWriteCheck_UsesBindingLookup is the regression test for
// review finding #1+#2: checks must go out under the per-repo
// installation's token, not the hardcoded installation_id=1.
// The fake fetcher records which install id it was called with; we
// assert it matches the binding lookup's id, not a constant.
func TestWriteCheck_UsesBindingLookup(t *testing.T) {
	var fetchedInstall atomic.Int64
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer fake.Close()

	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, id int64) (string, time.Time, error) {
		fetchedInstall.Store(id)
		return "ghs_test", time.Now().Add(time.Hour), nil
	}), time.Minute)
	b := &fakeBindings{id: 9876}
	c, err := NewChecksAPI(tokens, &singleHostClient{base: fake.Client(), api: fake.URL}, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteCheck(context.Background(), "octo/api", "deadbeef", githubdgrpc.CheckPhaseQueued, "", "queued"); err != nil {
		t.Fatal(err)
	}
	if fetchedInstall.Load() != 9876 {
		t.Errorf("fetched install = %d, want 9876 (the binding lookup id, not 1)", fetchedInstall.Load())
	}
	if b.gotRepo != "octo/api" {
		t.Errorf("lookup repo = %q, want octo/api", b.gotRepo)
	}
}

// TestWriteCheck_NoBindingFailsClosed asserts the §11 fail-closed
// behavior: when no app is bound to the repo, WriteCheck returns
// an error instead of falling back to installation_id=1 (which
// would send another customer's check-run under the wrong install).
func TestWriteCheck_NoBindingFailsClosed(t *testing.T) {
	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, _ int64) (string, time.Time, error) {
		t.Fatal("token cache must not be hit when bindings lookup misses")
		return "", time.Time{}, nil
	}), time.Minute)
	c, err := NewChecksAPI(tokens, http.DefaultClient, &fakeBindings{err: ErrNoBinding})
	if err != nil {
		t.Fatal(err)
	}
	err = c.WriteCheck(context.Background(), "octo/api", "deadbeef", githubdgrpc.CheckPhaseQueued, "", "queued")
	if err == nil {
		t.Fatal("expected error when no app is bound, got nil")
	}
	if !strings.Contains(err.Error(), "no app bound") {
		t.Errorf("err = %v, want 'no app bound' message", err)
	}
}

// newPreviewChecksAPI builds a ChecksAPI wired to a fake HTTP
// server + a fake binding + a fake token fetcher. Returns the
// ChecksAPI, the fake server URL, and a record of the body the
// server saw. Used by the WritePreviewCheck + WritePreviewCheckForkRefused
// tests below.
func newPreviewChecksAPI(t *testing.T) (*ChecksAPI, *atomic.Pointer[map[string]any]) {
	t.Helper()
	var gotBody atomic.Pointer[map[string]any]
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		_ = json.Unmarshal(body, &m)
		gotBody.Store(&m)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7777}`))
	}))
	t.Cleanup(fake.Close)

	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, _ int64) (string, time.Time, error) {
		return "ghs_preview_token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	c, err := NewChecksAPI(tokens, &singleHostClient{base: fake.Client(), api: fake.URL}, &fakeBindings{id: 99})
	if err != nil {
		t.Fatalf("NewChecksAPI: %v", err)
	}
	return c, &gotBody
}

// TestWritePreviewCheck_HappyPath_IncludesURL covers the
// production path: a PR-preview event with a known preview URL
// posts a Check Run named "gregale-preview" with a Markdown
// link to the preview in the summary, status=in_progress for
// the Building phase.
func TestWritePreviewCheck_HappyPath_IncludesURL(t *testing.T) {
	c, gotBody := newPreviewChecksAPI(t)
	previewURL := "https://pr-42-hello.apps.gregale.dev"
	err := c.WritePreviewCheck(context.Background(),
		"octo/api", "deadbeef", githubdgrpc.CheckPhaseBuilding,
		previewURL, "Building preview app for PR #42")
	if err != nil {
		t.Fatalf("WritePreviewCheck: %v", err)
	}
	body := *gotBody.Load()
	if body == nil {
		t.Fatal("no body captured")
	}
	if body["name"] != previewCheckName {
		t.Errorf("name = %v, want %q", body["name"], previewCheckName)
	}
	if body["status"] != statusInProgress {
		t.Errorf("status = %v, want %q", body["status"], statusInProgress)
	}
	if body["conclusion"] != nil {
		t.Errorf("conclusion = %v, want absent for in_progress", body["conclusion"])
	}
	summary, _ := body["output"].(map[string]any)["summary"].(string)
	if !strings.Contains(summary, previewURL) {
		t.Errorf("summary = %q, want it to contain %q", summary, previewURL)
	}
	if !strings.Contains(summary, "Building preview") {
		t.Errorf("summary = %q, want it to contain 'Building preview'", summary)
	}
}

// TestWritePreviewCheck_NoURL covers the URL-less path (D3 fork
// refusal in the dispatcher would NOT use this function —
// WritePreviewCheckForkRefused handles that — but the
// dispatcher's quota-exhausted path may not have a URL when
// CreateApp failed before the app row was written).
func TestWritePreviewCheck_NoURL(t *testing.T) {
	c, gotBody := newPreviewChecksAPI(t)
	if err := c.WritePreviewCheck(context.Background(),
		"octo/api", "deadbeef", githubdgrpc.CheckPhaseQueued,
		"", "Preview queued"); err != nil {
		t.Fatalf("WritePreviewCheck: %v", err)
	}
	body := *gotBody.Load()
	summary, _ := body["output"].(map[string]any)["summary"].(string)
	if strings.Contains(summary, "Preview URL:") {
		t.Errorf("summary = %q, want no Preview URL line when previewURL is empty", summary)
	}
	if !strings.Contains(summary, "Preview queued") {
		t.Errorf("summary = %q, want it to retain caller summary", summary)
	}
}

// TestWritePreviewCheck_RejectsMissingArgs mirrors the production
// WriteCheck guard: empty repo / sha returns a clear error rather
// than a downstream 4xx.
func TestWritePreviewCheck_RejectsMissingArgs(t *testing.T) {
	c, _ := newPreviewChecksAPI(t)
	if err := c.WritePreviewCheck(context.Background(), "", "sha", githubdgrpc.CheckPhaseQueued, "", ""); err == nil {
		t.Error("empty repo should error")
	}
	if err := c.WritePreviewCheck(context.Background(), "owner/repo", "", githubdgrpc.CheckPhaseQueued, "", ""); err == nil {
		t.Error("empty sha should error")
	}
}

// TestWritePreviewCheckForkRefused_HappyPath covers the D3
// neutral-check shape: status=completed, conclusion=neutral,
// title="Preview skipped (security policy)".
func TestWritePreviewCheckForkRefused_HappyPath(t *testing.T) {
	c, gotBody := newPreviewChecksAPI(t)
	err := c.WritePreviewCheckForkRefused(context.Background(),
		"octo/api", "deadbeef",
		"Fork PR refused — head repo differs from base repo")
	if err != nil {
		t.Fatalf("WritePreviewCheckForkRefused: %v", err)
	}
	body := *gotBody.Load()
	if body == nil {
		t.Fatal("no body captured")
	}
	if body["name"] != previewCheckName {
		t.Errorf("name = %v, want %q", body["name"], previewCheckName)
	}
	if body["status"] != statusCompleted {
		t.Errorf("status = %v, want %q", body["status"], statusCompleted)
	}
	if body["conclusion"] != previewCheckConclusionNeutral {
		t.Errorf("conclusion = %v, want %q", body["conclusion"], previewCheckConclusionNeutral)
	}
	if body["head_sha"] != "deadbeef" {
		t.Errorf("head_sha = %v, want deadbeef", body["head_sha"])
	}
	output, _ := body["output"].(map[string]any)
	if title, _ := output["title"].(string); title != "Preview skipped (security policy)" {
		t.Errorf("title = %q, want %q", title, "Preview skipped (security policy)")
	}
	summary, _ := output["summary"].(string)
	if !strings.Contains(summary, "Fork PR refused") {
		t.Errorf("summary = %q, want it to retain caller summary", summary)
	}
}

func TestWriteSkippedCheckForInstallation_HappyPath(t *testing.T) {
	c, gotBody := newPreviewChecksAPI(t)
	err := c.WriteSkippedCheckForInstallation(context.Background(), 99, "octo/api", "deadbeef", "Deployment skipped by commit marker [skip deploy].")
	if err != nil {
		t.Fatalf("WriteSkippedCheckForInstallation: %v", err)
	}
	body := *gotBody.Load()
	if body["name"] != prodCheckName {
		t.Errorf("name = %v, want %q", body["name"], prodCheckName)
	}
	if body["status"] != statusCompleted || body["conclusion"] != previewCheckConclusionNeutral {
		t.Errorf("status/conclusion = %v/%v, want completed/neutral", body["status"], body["conclusion"])
	}
	output, _ := body["output"].(map[string]any)
	if output["title"] != "Deployment skipped" || !strings.Contains(output["summary"].(string), "[skip deploy]") {
		t.Errorf("output = %v, want deployment skipped marker summary", output)
	}
}

// TestWritePreviewCheckForkRefused_RejectsMissingArgs mirrors
// the production guard.
func TestWritePreviewCheckForkRefused_RejectsMissingArgs(t *testing.T) {
	c, _ := newPreviewChecksAPI(t)
	if err := c.WritePreviewCheckForkRefused(context.Background(), "", "sha", "x"); err == nil {
		t.Error("empty repo should error")
	}
	if err := c.WritePreviewCheckForkRefused(context.Background(), "owner/repo", "", "x"); err == nil {
		t.Error("empty sha should error")
	}
}

// newDestroyCommentChecksAPI is the destroy-comment twin of
// newPreviewChecksAPI: same wiring, but the fake server records
// the request path so a test can assert the comment endpoint
// (POST /repos/{o}/{r}/issues/{n}/comments) was hit, not the
// check-run endpoint.
func newDestroyCommentChecksAPI(t *testing.T) (*ChecksAPI, *atomic.Pointer[map[string]any], *atomic.Pointer[string]) {
	t.Helper()
	var gotBody atomic.Pointer[map[string]any]
	var gotPath atomic.Pointer[string]
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(&r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		_ = json.Unmarshal(body, &m)
		gotBody.Store(&m)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1234}`))
	}))
	t.Cleanup(fake.Close)

	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, _ int64) (string, time.Time, error) {
		return "ghs_destroy_token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	c, err := NewChecksAPI(tokens, &singleHostClient{base: fake.Client(), api: fake.URL}, &fakeBindings{id: 99})
	if err != nil {
		t.Fatalf("NewChecksAPI: %v", err)
	}
	return c, &gotBody, &gotPath
}

// TestWritePreviewDestroyComment_HappyPath confirms the
// issue-comment endpoint (NOT the check-run endpoint) is hit,
// the body is echoed as a Markdown fragment, and the request
// succeeds. Pins the route shape that pkg/githubd/service.go's
// close-arm depends on.
func TestWritePreviewDestroyComment_HappyPath(t *testing.T) {
	c, gotBody, gotPath := newDestroyCommentChecksAPI(t)
	body := "Preview `pr-42-hello` is open. [Tear it down](/dashboard/apps/hello/preview/pr-42-hello/destroy)."
	if err := c.WritePreviewDestroyComment(context.Background(),
		"octo/api", 42, body); err != nil {
		t.Fatalf("WritePreviewDestroyComment: %v", err)
	}
	wantPath := "/repos/octo/api/issues/42/comments"
	if p := gotPath.Load(); p == nil || *p != wantPath {
		t.Errorf("path = %v, want %q (must hit the issue-comment endpoint, not check-runs)", p, wantPath)
	}
	captured := gotBody.Load()
	if captured == nil {
		t.Fatal("no body captured")
	}
	if (*captured)["body"] != body {
		t.Errorf("body = %v, want %q (Markdown fragment must be echoed verbatim)", (*captured)["body"], body)
	}
}

// TestWritePreviewDestroyComment_RejectsMissingArgs mirrors
// the production guard: empty repo / zero pr_number both error
// before any HTTP request goes out.
func TestWritePreviewDestroyComment_RejectsMissingArgs(t *testing.T) {
	c, _, _ := newDestroyCommentChecksAPI(t)
	if err := c.WritePreviewDestroyComment(context.Background(), "", 42, "x"); err == nil {
		t.Error("empty repo should error")
	}
	if err := c.WritePreviewDestroyComment(context.Background(), "owner/repo", 0, "x"); err == nil {
		t.Error("zero pr_number should error")
	}
	if err := c.WritePreviewDestroyComment(context.Background(), "owner/repo", -1, "x"); err == nil {
		t.Error("negative pr_number should error")
	}
}

// TestWritePreviewDestroyComment_HTTPErrorReturnsErr confirms
// GitHub's non-2xx response surfaces as an error from
// WritePreviewDestroyComment. The body is included in the
// error so the operator can diagnose without re-issuing the
// call.
func TestWritePreviewDestroyComment_HTTPErrorReturnsErr(t *testing.T) {
	var gotPath atomic.Pointer[string]
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(&r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"rate limit exceeded"}`))
	}))
	t.Cleanup(fake.Close)
	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, _ int64) (string, time.Time, error) {
		return "ghs_t", time.Now().Add(time.Hour), nil
	}), time.Minute)
	c, err := NewChecksAPI(tokens, &singleHostClient{base: fake.Client(), api: fake.URL}, &fakeBindings{id: 99})
	if err != nil {
		t.Fatal(err)
	}
	err = c.WritePreviewDestroyComment(context.Background(), "octo/api", 42, "preview body")
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want it to include status code 403", err)
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("err = %v, want it to include the response body", err)
	}
	if p := gotPath.Load(); p == nil || *p != "/repos/octo/api/issues/42/comments" {
		t.Errorf("path = %v, want /repos/octo/api/issues/42/comments", p)
	}
}

func TestUpsertPreviewComment_CreatesThenPatchesMarkedComment(t *testing.T) {
	var calls []string
	var bodies []string
	marker := "<!-- gregale-preview:pr-42-demo-app -->"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		payload, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(payload))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if len(calls) == 1 {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":987,"body":"` + marker + `\nold"}]`))
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":987}`))
		case http.MethodPatch:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":987}`))
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(fake.Close)
	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, _ int64) (string, time.Time, error) {
		return "ghs_preview_token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	c, err := NewChecksAPI(tokens, &singleHostClient{base: fake.Client(), api: fake.URL}, &fakeBindings{id: 99})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertPreviewComment(context.Background(), 99, "octo/api", 42, marker, "new status"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.UpsertPreviewComment(context.Background(), 99, "octo/api", 42, marker, "updated status"); err != nil {
		t.Fatalf("patch: %v", err)
	}
	wantCalls := []string{
		"GET /repos/octo/api/issues/42/comments",
		"POST /repos/octo/api/issues/42/comments",
		"GET /repos/octo/api/issues/42/comments",
		"PATCH /repos/octo/api/issues/comments/987",
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if !strings.Contains(bodies[1], "new status") || !strings.Contains(bodies[3], "updated status") {
		t.Fatalf("bodies = %v, want create and updated bodies", bodies)
	}
}

func TestUpsertPreviewComment_RejectsMissingArgs(t *testing.T) {
	c := &ChecksAPI{HTTP: http.DefaultClient}
	if err := c.UpsertPreviewComment(context.Background(), 0, "owner/repo", 1, "m", "b"); err == nil {
		t.Error("zero installation should error")
	}
	if err := c.UpsertPreviewComment(context.Background(), 1, "", 1, "m", "b"); err == nil {
		t.Error("empty repo should error")
	}
}
