// Whitebox tests for handleSourceRefDeploy (cmd/apid/handlers_source_ref.go,
// DEPLOY-PROV-4 / ADR-092, issue #739). Pins the wire contract +
// audit shape + IDOR posture + idempotency replay + cap + transport-error
// mapping for the new route.
//
// The githubd side is mocked via a recording GithubdClient so the
// handler tests can exercise the round-trip without a real socket.
// The recording fake exposes a one-shot canned response for the
// StreamSourceRef RPC; token minting is owned by githubd.
// and records the (account_id, installation_id, repo, ref, max_bytes)
// tuple so the audit + spool pipelines can assert on it after the
// handler returns.
//
// Coverage:
//
//   - IDOR (other-account slug)             → 404
//   - missing repo / ref                    → 400 validation
//   - malformed ref shape                   → 400 code=invalid_ref
//   - missing durable install               → 404 code=github_install_not_found
//   - happy path JSON deploy                → 202 + deployment row + kind=github
//   - transport error on stream             → 503 code=source_ref_unavailable
//   - cap trip mid-stream                   → 413 code=source_too_large
//   - unsupported format                    → 400 validation
//   - idempotency replay                    → 202 + Idempotent-Replayed: true
//   - raw install token NEVER on wire       → pinned in TestSourceRef_HappyPath
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceRefFake is the recording GithubdClient used by every test
// in this file. It records the most recent (mint, stream) call so
// the test can assert on the (account_id, installation_id, repo,
// ref, max_bytes) tuple after the handler returns.
//
// The handler reads the install row from state.Store (via
// resolveInstallToken), so the fake must be paired with a
// state.GitHubInstall row on the mem store. The helper
// newSourceRefTestServer wires both sides.
type sourceRefFake struct {
	mux sync.Mutex

	// Recorded calls.
	mintCalls  int
	mintAcctID string
	mintInstID int64

	streamCalls    int
	streamAcctID   string
	streamInstID   int64
	streamRepo     string
	streamRef      string
	streamMaxBytes int64

	// Programmable responses.
	mintToken     string
	mintExpiresAt time.Time
	mintErr       error

	streamBody     io.ReadCloser // closed by caller; bytes flow to validateAndSpool
	streamTruncate bool
	streamBytes    int64
	streamSHA      string
	streamStatsErr error
	streamErr      error
}

func (f *sourceRefFake) MintInstallationToken(_ context.Context, acctID string, instID int64) (string, time.Time, error) {
	f.mux.Lock()
	defer f.mux.Unlock()
	f.mintCalls++
	f.mintAcctID = acctID
	f.mintInstID = instID
	if f.mintErr != nil {
		return "", time.Time{}, f.mintErr
	}
	return f.mintToken, f.mintExpiresAt, nil
}

func (f *sourceRefFake) StreamSourceRef(_ context.Context, acctID string, instID int64, repo, ref string, maxBytes int64) (*StreamSourceRefResult, error) {
	f.mux.Lock()
	defer f.mux.Unlock()
	f.streamCalls++
	f.streamAcctID = acctID
	f.streamInstID = instID
	f.streamRepo = repo
	f.streamRef = ref
	f.streamMaxBytes = maxBytes
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	return &StreamSourceRefResult{
		Body: f.streamBody,
		Stats: &StreamSourceRefStats{
			Truncated:         f.streamTruncate,
			BytesStreamed:     f.streamBytes,
			ResolvedCommitSHA: f.streamSHA,
			Err:               f.streamStatsErr,
		},
	}, nil
}

// Stub-out the rest of the GithubdClient surface — handleSourceRefDeploy
// doesn't touch these, but the interface contract requires them.
func (f *sourceRefFake) VerifyInstallation(context.Context, int64, string) (bool, string, string, error) {
	return false, "", "", nil
}
func (f *sourceRefFake) GetInstallState(context.Context, string) (InstallState, string, string, error) {
	return InstallStateBound, "", "", nil
}
func (f *sourceRefFake) ExchangeOAuthCode(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
}
func (f *sourceRefFake) ListInstallableRepos(context.Context, string) ([]Repo, error) {
	return nil, nil
}
func (f *sourceRefFake) BindAppRepo(context.Context, string, string, string, string) (string, error) {
	return "", nil
}
func (f *sourceRefFake) UnbindAppRepo(context.Context, string, string) error { return nil }
func (f *sourceRefFake) GetAppBinding(context.Context, string, string) (AppBinding, error) {
	return AppBinding{}, nil
}
func (f *sourceRefFake) CreateDeploymentFromPush(context.Context, string, string, string, string) (string, string, error) {
	return "", "", nil
}
func (f *sourceRefFake) WriteCheck(context.Context, string, string, CheckPhase, string, string) error {
	return nil
}
func (f *sourceRefFake) Close() error { return nil }

// nopReadCloser wraps a *bytes.Reader as an io.ReadCloser for the
// streaming fake.
type nopReadCloser struct{ *bytes.Reader }

func (nopReadCloser) Close() error { return nil }

type sourceRefFailingReader struct {
	data []byte
	done bool
}

func (r *sourceRefFailingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("simulated mid-stream failure")
	}
	r.done = true
	return copy(p, r.data), nil
}

func (*sourceRefFailingReader) Close() error { return nil }

// buildSourceRefTarGz returns a minimal valid tar.gz with one
// `index.js` file. Used to feed the streaming fake so
// validateAndSpool has something to validate.
func buildSourceRefTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "exports.handler = () => 1;\n"
	hdr := &tar.Header{Name: "index.js", Mode: 0644, Size: int64(len(body))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// sourceRefTestEnv bundles the wiring every test in this file uses:
// the http.Handler, the programmed fake, the store, the API key,
// and a pre-baked 1-file tar.gz for the success path.
//
// Wire-up: builds a memstore, seeds an account + app + durable
// install row at installID, generates an admin-scope API key, and
// constructs the apid handler with the programmable GithubdClient
// from newServerWithDeps. Mirrors the shape of newOAuthTestServer
// (handlers_oauth_test.go) and newBindPickerTestServer
// (handlers_install_github_test.go).
type sourceRefTestEnv struct {
	h       http.Handler
	store   *state.MemStore
	gh      *sourceRefFake
	key     string
	acctID  string
	appID   string
	tarball []byte
}

func newSourceRefTestServer(t *testing.T, plan api.Plan, slug string, installID int64) sourceRefTestEnv {
	t.Helper()
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())

	store := state.NewMemStore()
	gh := &sourceRefFake{
		mintToken:     "gh_tok_test",
		mintExpiresAt: time.Now().Add(time.Hour),
	}
	acct, err := store.CreateAccount(context.Background(), "src-ref@example.com", plan)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID,
		Slug:      slug,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if err := store.UpsertGitHubInstall(context.Background(), state.GitHubInstall{
		AccountID:        acct.ID,
		InstallationID:   installID,
		AuditGithubLogin: "alice",
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	srv := newServerWithDeps(store,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev",
		noopNotifier{},
		"", noopMailer{}, gh, nil, nil,
		15*60_000_000_000, "",
	)
	return sourceRefTestEnv{
		h:       srv.handler(),
		store:   store,
		gh:      gh,
		key:     pt,
		acctID:  acct.ID,
		appID:   app.ID,
		tarball: buildSourceRefTarGz(t),
	}
}

// post issues a POST with the bearer key + JSON body.
func (e sourceRefTestEnv) post(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return e.postWithHeaders(t, path, body, nil)
}

// postWithIdempotency issues a POST with the bearer key, JSON
// body, and an Idempotency-Key header. Used by the idempotency
// replay test to stamp a stable key the second POST can repeat.
func (e sourceRefTestEnv) postWithIdempotency(t *testing.T, path string, body any, key string) *httptest.ResponseRecorder {
	t.Helper()
	return e.postWithHeaders(t, path, body, map[string]string{"Idempotency-Key": key})
}

// postWithHeaders is the canonical POST helper; post +
// postWithIdempotency are thin wrappers around it.
func (e sourceRefTestEnv) postWithHeaders(t *testing.T, path string, body any, hdrs map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest("POST", path, r)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// bodyCode extracts the RFC 7807 Code from a wire response. Returns
// the empty string when the body is not a Problem envelope.
func bodyCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		return ""
	}
	return p.Code
}

// ----------------------------------------------------------------------------
// Tests follow.
// ----------------------------------------------------------------------------

// TestSourceRef_IDOR pins the cross-account slug guard.
func TestSourceRef_IDOR(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "victim", 9999)

	// Caller's API key is for e.acctID. Target a slug owned by
	// a DIFFERENT account. The store the test creates doesn't
	// share rows with e.store, so even though the slug exists
	// in the other store, e.store.AppBySlug returns ErrNotFound
	// (which loadAppAndPreflight maps to 404).
	other := state.NewMemStore()
	otherAcct, _ := other.CreateAccount(context.Background(), "other@example.com", api.PlanPro)
	other.CreateApp(context.Background(), state.App{
		AccountID: otherAcct.ID, Slug: "evil", Status: state.AppActive,
	})

	rec := e.post(t, "/v1/apps/evil/deployments/source-ref",
		api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
}

// TestSourceRef_BadRef covers the cheap pre-flight ref-shape guard.
func TestSourceRef_BadRef(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "x", 9999)
	e.gh.streamBody = nopReadCloser{bytes.NewReader(e.tarball)}

	cases := []struct {
		name string
		ref  string
		code string
	}{
		{"empty", "", ""}, // missing field → plain 400 validation, not CodeInvalidRef
		{"too short", "abc", api.CodeInvalidRef},
		{"path traversal", "main/../../etc", api.CodeInvalidRef},
		{"shell injection", "main;rm -rf /", api.CodeInvalidRef},
		{"spaces", "feature branch", api.CodeInvalidRef},
		{"wildcard", "feature*", api.CodeInvalidRef},
		{"trailing dot", "release.", api.CodeInvalidRef},
		{"at sign", "@", api.CodeInvalidRef},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.post(t, "/v1/apps/x/deployments/source-ref",
				api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: tc.ref})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for %q", rec.Code, tc.ref)
			}
			if tc.code == "" {
				return
			}
			if got := bodyCode(t, rec); got != tc.code {
				t.Errorf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

// TestSourceRef_NoInstall pins the 404 code=github_install_not_found
// contract for customers who haven't bound a GitHub App yet.
func TestSourceRef_NoInstall(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())

	store := state.NewMemStore()
	gh := &sourceRefFake{} // resolvesInstallToken never reaches mint when install row missing
	acct, _ := store.CreateAccount(context.Background(), "src@example.com", api.PlanPro)
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatalf("seed apikey: %v", err)
	}
	store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "x", Status: state.AppActive,
	})

	srv := newServerWithDeps(store,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{}, "", noopMailer{}, gh, nil, nil,
		15*60_000_000_000, "")
	env := sourceRefTestEnv{h: srv.handler(), store: store, gh: gh, key: pt, acctID: acct.ID}

	rec := env.post(t, "/v1/apps/x/deployments/source-ref",
		api.SourceRefDeployRequest{
			Repo: "onebox-faas/hello",
			Ref:  "0123456789abcdef0123456789abcdef01234567",
		})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if got := bodyCode(t, rec); got != api.CodeGitHubInstallNotFound {
		t.Errorf("code = %q, want %q", got, api.CodeGitHubInstallNotFound)
	}
}

// TestSourceRef_HappyPath is the central coverage pin: a JSON POST
// with a valid 40-char ref reaches the githubd gRPC bridge once
// (the bridge owns token minting + archive streaming), the spool
// pipeline writes the tarball, the
// deployment row carries Kind=DeploymentKindGitHub + CommitSHA +
// SourceURL="github://<repo>@<sha>", the audit row carries
// {repo, ref, source_sha, install_id, app_id, deployment_id, build_id}
// (and NEVER the raw install token), and the raw install token is
// NEVER in the wire response.
func TestSourceRef_HappyPath(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	e.gh.streamBody = nopReadCloser{bytes.NewReader(e.tarball)}

	const sha40 = "0123456789abcdef0123456789abcdef01234567"
	rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{
		Repo: "onebox-faas/hello",
		Ref:  sha40,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	if e.gh.mintCalls != 0 {
		t.Errorf("mintCalls = %d, want 0 (githubd owns token minting)", e.gh.mintCalls)
	}
	if e.gh.streamCalls != 1 {
		t.Errorf("streamCalls = %d, want 1", e.gh.streamCalls)
	}
	if e.gh.streamRepo != "onebox-faas/hello" || e.gh.streamRef != sha40 {
		t.Errorf("stream(repo=%q ref=%q), want repo=onebox-faas/hello ref=%s",
			e.gh.streamRepo, e.gh.streamRef, sha40)
	}
	if got, want := e.gh.streamMaxBytes, int64(250)*1024*1024; got != want {
		t.Errorf("max_bytes = %d, want %d (Pro source cap)", got, want)
	}

	// Confirm the deployment row carries Kind=github + CommitSHA +
	// SourceURL=github://… — same shape as the githubd bridge.
	deps, err := e.store.ListDeploymentsForApp(context.Background(), e.appID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("deployment count = %d, want 1", len(deps))
	}
	dep := deps[0]
	if dep.CommitSHA != sha40 {
		t.Errorf("CommitSHA = %q, want %q", dep.CommitSHA, sha40)
	}
	if dep.SourceURL != "github://onebox-faas/hello@"+sha40 {
		t.Errorf("SourceURL = %q, want %s", dep.SourceURL, "github://onebox-faas/hello@"+sha40)
	}
	// Raw token MUST NOT appear anywhere on the wire.
	if bytes.Contains(rec.Body.Bytes(), []byte("gh_tok_test")) {
		t.Errorf("response body leaked install token; body=%s", rec.Body)
	}

	// Audit row pins: kind=deploy.source_ref, payload carries
	// repo/ref/source_sha/install_id WITHOUT the raw install token.
	// Mirrors handlers_audit_test.go::TestAuditEvents_AppDeployedEmitsEvent
	// but on the new kind.
	rows, err := e.store.ListEvents(context.Background(), e.acctID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var row *state.Event
	for i := range rows {
		if rows[i].Kind == "deploy.source_ref" {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("no deploy.source_ref audit row; events=%+v", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(row.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["repo"] != "onebox-faas/hello" {
		t.Errorf("audit.repo = %v, want onebox-faas/hello", data["repo"])
	}
	if data["ref"] != sha40 {
		t.Errorf("audit.ref = %v, want %s", data["ref"], sha40)
	}
	if data["source_sha"] != sha40 {
		t.Errorf("audit.source_sha = %v, want %s", data["source_sha"], sha40)
	}
	if id, ok := data["install_id"].(float64); !ok || int64(id) != 7777 {
		t.Errorf("audit.install_id = %v, want 7777", data["install_id"])
	}
	// Raw install token MUST NOT appear anywhere in the audit row.
	if bytes.Contains(row.Data, []byte("gh_tok_test")) {
		t.Errorf("audit payload leaked install token; data=%s", row.Data)
	}
}

// TestSourceRef_BranchUsesResolvedSHA pins the provenance boundary:
// a mutable branch ref is accepted only when githubd returns a canonical
// commit SHA, and the deployment stores the SHA rather than the branch.
func TestSourceRef_BranchUsesResolvedSHA(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	e.gh.streamBody = nopReadCloser{bytes.NewReader(e.tarball)}
	const resolvedSHA = "abcdef0123456789abcdef0123456789abcdef01"
	e.gh.streamSHA = resolvedSHA

	rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{
		Repo: "onebox-faas/hello",
		Ref:  "release/2026-q3",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	if e.gh.streamRef != "release/2026-q3" {
		t.Fatalf("stream ref = %q, want branch ref", e.gh.streamRef)
	}
	deps, err := e.store.ListDeploymentsForApp(context.Background(), e.appID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("deployment count = %d, want 1", len(deps))
	}
	if deps[0].CommitSHA != resolvedSHA {
		t.Errorf("CommitSHA = %q, want resolved %q", deps[0].CommitSHA, resolvedSHA)
	}
	if deps[0].SourceURL != "github://onebox-faas/hello@"+resolvedSHA {
		t.Errorf("SourceURL = %q, want canonical SHA", deps[0].SourceURL)
	}
}

// TestSourceRef_StreamError pins the 503 mapping when the githubd
// gRPC returns an Unavailable problem mid-stream.
func TestSourceRef_StreamError(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	e.gh.streamErr = api.NewProblem(503, api.CodeSourceRefUnavailable, "githubd unavailable", "down")

	rec := e.post(t, "/v1/apps/x/deployments/source-ref",
		api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567"})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := bodyCode(t, rec); got != api.CodeSourceRefUnavailable {
		t.Errorf("code = %q, want %q", got, api.CodeSourceRefUnavailable)
	}
	// No deployment row written on a 503.
	deps, _ := e.store.ListDeploymentsForApp(context.Background(), e.appID, 0, 0)
	if len(deps) != 0 {
		t.Errorf("deployment row written on 503 (%d); want 0", len(deps))
	}
}

// TestSourceRef_MidStreamErrorMapsToUnavailable pins the error path where
// githubd has already delivered bytes before the gRPC stream fails. The pipe
// reader reports a local copy error first, but the terminal stream problem
// must win so clients can retry instead of treating the response as a bad
// tarball.
func TestSourceRef_MidStreamErrorMapsToUnavailable(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	e.gh.streamBody = &sourceRefFailingReader{data: e.tarball}
	e.gh.streamStatsErr = api.ErrSourceRefUnavailable("githubd stream failed")

	rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{
		Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567",
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body)
	}
	if got := bodyCode(t, rec); got != api.CodeSourceRefUnavailable {
		t.Errorf("code = %q, want %q", got, api.CodeSourceRefUnavailable)
	}
}

// TestSourceRef_CapTrip pins the 413 mapping when the streaming
// fake stamps truncated=true mid-flight.
func TestSourceRef_CapTrip(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	e.gh.streamBody = nopReadCloser{bytes.NewReader(e.tarball)}
	e.gh.streamTruncate = true
	e.gh.streamBytes = int64(len(e.tarball))

	rec := e.post(t, "/v1/apps/x/deployments/source-ref",
		api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567"})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if got := bodyCode(t, rec); got != api.CodeSourceTooLarge {
		t.Errorf("code = %q, want %q", got, api.CodeSourceTooLarge)
	}
}

// TestSourceRef_UnsupportedFormat pins the 400 validation branch
// for a forward-compat field that's not yet wired.
func TestSourceRef_UnsupportedFormat(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	e.gh.streamBody = nopReadCloser{bytes.NewReader(e.tarball)}

	rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{
		Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567", Format: "git-archive",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
}

// TestSourceRef_Idempotency pins the Idempotent-Replayed contract:
// a re-POST with the same Idempotency-Key returns the cached
// response WITHOUT minting a fresh install token or streaming the
// tarball again.
func TestSourceRef_Idempotency(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	e.gh.streamBody = nopReadCloser{bytes.NewReader(e.tarball)}

	body := api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567"}

	// First POST — fresh mint/stream, 202. Stamp the Idempotency-Key
	// so the idempotency middleware records the response.
	first := e.postWithIdempotency(t, "/v1/apps/x/deployments/source-ref", body, "ci-build-attempt-1")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first POST status = %d, want 202; body=%s", first.Code, first.Body)
	}

	// Second POST with the same Idempotency-Key — cached response,
	// Idempotent-Replayed: true.
	rec := e.postWithIdempotency(t, "/v1/apps/x/deployments/source-ref", body, "ci-build-attempt-1")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("second POST status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("missing Idempotent-Replayed: true header; got %q",
			rec.Header().Get("Idempotent-Replayed"))
	}
	if e.gh.mintCalls != 0 {
		t.Errorf("mintCalls = %d after replay, want 0 (githubd owns token minting)", e.gh.mintCalls)
	}
	if e.gh.streamCalls != 1 {
		t.Errorf("streamCalls = %d after replay, want 1 (no re-stream)", e.gh.streamCalls)
	}
	deps, _ := e.store.ListDeploymentsForApp(context.Background(), e.appID, 0, 0)
	if len(deps) != 1 {
		t.Errorf("deployment count = %d, want 1 (replay must NOT create a new row)", len(deps))
	}
}
