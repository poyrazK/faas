package githubd

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// stubBindings is a hand-rolled fake of AppBindingStore. PR-H widens
// the return type to state.GitHubBinding so the push-dispatch path
// can read AccountID + InstallID without a second round-trip. Tests
// populate the bind row via the same shape the production adapter
// returns, so the fake stays a faithful mirror.
type stubBindings struct {
	byRepo map[string]state.GitHubBinding // key: "owner/repo|branch"
	err    error
}

func (s *stubBindings) GetAppBinding(_ context.Context, repo, branch string) (state.GitHubBinding, error) {
	if s.err != nil {
		return state.GitHubBinding{}, s.err
	}
	return s.byRepo[repo+"|"+branch], nil
}

// stubInstalls is the fake InstallsLookup. The githubd push path
// resolves the durable install row by account ID (PR-H's chosen
// key). Errors here surface as 5xx; ErrNotFound surfaces as
// ErrNoBinding so the test suite can pin the no-binding fall-
// through.
type stubInstalls struct {
	byAccount map[string]state.GitHubInstall
	err       error
}

func (s *stubInstalls) ForAccount(_ context.Context, accountID string) (state.GitHubInstall, error) {
	if s.err != nil {
		return state.GitHubInstall{}, s.err
	}
	return s.byAccount[accountID], nil
}

// stubSourceTree satisfies SourceTree. The reconciler pulls repos
// off the FS so the fake exposes the same fstest.MapFS a real
// archive extraction would produce. Close is a no-op — tests don't
// allocate a temp dir.
type stubSourceTree struct {
	fsys fs.FS
}

func (s *stubSourceTree) FS() fs.FS    { return s.fsys }
func (s *stubSourceTree) Close() error { return nil }

// stubSource is the fake SourceFetcher. It returns the test's
// canned MapFS for any (installID, repo, sha) triple; tests that
// need a fetch failure inject an err.
type stubSource struct {
	fsys fs.FS
	err  error
}

func (s *stubSource) Fetch(_ context.Context, _ string, _ int64, _, _ string) (SourceTree, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &stubSourceTree{fsys: s.fsys}, nil
}

// testRig bundles the dependencies Service.HandlePushRequest needs
// so individual tests can override the slice they care about
// (e.g. drive the feature-branch guard by setting a non-prod
// branch via the test body, drive a Source error by setting
// source.err).
type testRig struct {
	mem     *state.MemStore
	auditor *audit.Auditor
	rec     *reconcile.Service
	acct    string
	install int64
}

func newRig(t *testing.T, scanFn func(fs.FS) (reposcan.Result, error)) *testRig {
	t.Helper()
	mem := state.NewMemStore()
	aud := audit.New(mem, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, "githubd_test")
	rec := reconcile.NewService(mem, aud, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if scanFn != nil {
		rec.Scan = scanFn
	}
	acct, err := mem.CreateAccount(context.Background(), "octo@example.com", "hobby")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return &testRig{mem: mem, auditor: aud, rec: rec, acct: acct.ID, install: 42}
}

// seedProject seeds the (installID, repo) → project row the push-
// dispatch path resolves in step 3. ScanSource is set to the
// Tier-1 "single" tier so the happy-path test (which stubs
// reposcan.Scan → Tier=1) doesn't trip the scan-source-stability
// guard. The ReconcileErrorBubbles test overrides the tier upward
// after seeding.
func (r *testRig) seedProject(t *testing.T, repo, prodBranch string) state.Project {
	t.Helper()
	p, err := r.mem.CreateProject(context.Background(), state.Project{
		AccountID:        r.acct,
		Slug:             "demo",
		InstallID:        r.install,
		RepoFullName:     repo,
		ProductionBranch: prodBranch,
		ScanSource:       state.ProjectScanSource("single"),
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return p
}

func happyScan() reposcan.Result {
	return reposcan.Result{
		Workloads: []reposcan.Workload{{Class: reposcan.ClassHTTP, Name: "api", RootDir: "."}},
		Managed:   []reposcan.Managed{},
		Tier:      1,
	}
}

func newServiceForRig(t *testing.T, r *testRig) *Service {
	t.Helper()
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{
		"octo/api|main": {BindingID: "b-1", AccountID: r.acct, InstallID: r.install, RepoFullName: "octo/api", ProductionBranch: "main"},
	}}
	svc.Installs = &stubInstalls{byAccount: map[string]state.GitHubInstall{
		r.acct: {AccountID: r.acct, InstallationID: r.install, DefaultBranch: "main"},
	}}
	// Issue #432 phase 5: the MapFS fixture now seeds files
	// under each workload's RootDir so the staging step
	// (pkg/githubd/staging.go) can walk the subtree and
	// produce a non-empty tarball. Without these, the
	// staging step returns ErrNotExist and the dispatcher
	// skips the enqueue. The "" RootDir (root-dir workload)
	// is satisfied by the docker-compose.yml at the repo
	// root.
	svc.Source = &stubSource{fsys: fstest.MapFS{
		"docker-compose.yml":             &fstest.MapFile{Data: []byte("version: '3'\nservices:\n  api:\n    build: .\n")},
		"services/auth/api/index.ts":     &fstest.MapFile{Data: []byte("export const auth = true;\n")},
		"services/auth/api/package.json": &fstest.MapFile{Data: []byte("{}\n")},
		"services/billing/main.go":       &fstest.MapFile{Data: []byte("package main\n")},
	}}
	svc.Reconcile = r.rec
	// Issue #432 phase 5: the staging step needs a workDir
	// (pkg/githubd/staging.go:stageAppSource). Tests get a
	// fresh tmpdir per call so parallel test runs don't race
	// on the staged tarball.
	svc.WorkDir = t.TempDir()
	return svc
}

func TestHandlePushRequest_HappyPath(t *testing.T) {
	// Seed the project with the matching scan tier (single) so the
	// scan-source-stability guard doesn't trip. The stub Scan
	// returns a Tier=1 result (one compose file → single).
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	var checkRepo, checkSHA string
	var checkPhase githubdgrpc.CheckPhase
	svc.WriteCheck = func(_ context.Context, repo, sha string, phase githubdgrpc.CheckPhase) error {
		checkRepo, checkSHA, checkPhase = repo, sha, phase
		return nil
	}
	body := []byte(`{"ref":"refs/heads/main","after":"cafebabe","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	result, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(result.Added) != 1 {
		t.Errorf("result.Added = %d, want 1", len(result.Added))
	}
	if checkRepo != "octo/api" || checkSHA != "cafebabe" || checkPhase != githubdgrpc.CheckPhaseQueued {
		t.Errorf("WriteCheck args = (%q,%q,%v)", checkRepo, checkSHA, checkPhase)
	}
}

func TestHandlePushRequest_NoBindingIsSilent(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{}}
	svc.Installs = &stubInstalls{byAccount: map[string]state.GitHubInstall{rig.acct: {AccountID: rig.acct, InstallationID: rig.install}}}
	svc.Source = &stubSource{fsys: fstest.MapFS{}}
	svc.Reconcile = rig.rec
	body := []byte(`{"ref":"refs/heads/main","after":"deadbeef","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !IsNoBinding(err) {
		t.Errorf("err = %v, want ErrNoBinding", err)
	}
}

func TestHandlePushRequest_FeatureBranchIgnored(t *testing.T) {
	// Seed a project whose production_branch="main". Push to
	// refs/heads/feature/x — bind matches via the feature/x row,
	// reconcile's productionBranchOnly guard trips, HandlePushRequest
	// returns ErrIgnored.
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	// Override the binding map to include the feature/x branch.
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{
		"octo/api|feature/x": {BindingID: "b-1", AccountID: rig.acct, InstallID: rig.install, RepoFullName: "octo/api", ProductionBranch: "main"},
	}}
	body := []byte(`{"ref":"refs/heads/feature/x","after":"x","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !IsIgnored(err) {
		t.Errorf("err = %v, want ErrIgnored", err)
	}
}

func TestHandlePushRequest_SourceFetchFailure(t *testing.T) {
	// Seed project + binding + install so the Source error fires
	// before any ProjectByRepo fall-through.
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	want := errors.New("codeload down")
	svc.Source = &stubSource{err: want}
	body := []byte(`{"ref":"refs/heads/main","after":"x","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "githubd: source fetch") {
		t.Errorf("err = %v, want op prefix 'githubd: source fetch'", err)
	}
}

func TestHandlePushRequest_ReconcileErrorBubbles(t *testing.T) {
	// Drive a real reconcile-package error: a scan-source downgrade
	// trips the scan-source-stability guard and Reconcile returns
	// a typed error. The githubd handler does NOT translate it to
	// ErrNoBinding or ErrIgnored, so the error bubbles.
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	// Seed with a higher-tier scan source (compose) so the stub
	// Scan's Tier=1 result is a downgrade.
	_, err := rig.mem.CreateProject(context.Background(), state.Project{
		AccountID:        rig.acct,
		Slug:             "demo",
		InstallID:        rig.install,
		RepoFullName:     "octo/api",
		ProductionBranch: "main",
		ScanSource:       state.ProjectScanSourceCompose,
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	svc := newServiceForRig(t, rig)
	body := []byte(`{"ref":"refs/heads/main","after":"x","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err = svc.HandlePushRequest(context.Background(), body)
	if err == nil {
		t.Fatal("expected scan-source-downgrade error, got nil")
	}
	if IsNoBinding(err) || IsIgnored(err) {
		t.Errorf("err = %v, must not be translated to ErrNoBinding/ErrIgnored", err)
	}
	if !errors.Is(err, state.ErrScanSourceDowngrade) {
		t.Errorf("err = %v, want state.ErrScanSourceDowngrade", err)
	}
}

func TestHandlePushRequest_TagDeploysAgainstDefaultBranch(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	body := []byte(`{"ref":"refs/tags/v1.0.0","before":"0000000000000000000000000000000000000000","after":"x","created":true,"repository":{"full_name":"octo/api","name":"api","default_branch":"main"},"pusher":{"name":"alice"}}`)
	result, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("tag push: %v", err)
	}
	if len(result.Added) != 1 {
		t.Errorf("tag result.Added = %d, want 1", len(result.Added))
	}
}

func TestHandlePushRequest_TagUsesConfiguredProductionBranch(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "release")
	svc := newServiceForRig(t, rig)
	// No default-branch binding: the project row must supply the
	// configured production branch instead of silently ignoring the
	// release tag.
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{}}
	body := []byte(`{"ref":"refs/tags/v1.1.0","before":"0000000000000000000000000000000000000000","after":"x","created":true,"repository":{"full_name":"octo/api","name":"api","default_branch":"main"},"installation":{"id":42},"pusher":{"name":"alice"}}`)
	if _, err := svc.HandlePushRequest(context.Background(), body); err != nil {
		t.Fatalf("tag push on configured branch: %v", err)
	}
}

func TestHandlePushRequest_TagDeletionIsIgnored(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	body := []byte(`{"ref":"refs/tags/v1.0","after":"0000000000000000000000000000000000000000","deleted":true,"repository":{"full_name":"octo/api","name":"api","default_branch":"main"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !IsNoBinding(err) {
		t.Errorf("tag deletion → err = %v, want ErrNoBinding", err)
	}
}

func TestValidateReleaseTag(t *testing.T) {
	const zero = "0000000000000000000000000000000000000000"
	tests := []struct {
		name    string
		tag     string
		before  string
		created bool
		forced  bool
		reason  string
	}{
		{name: "stable", tag: "v1.2.3", before: zero, created: true},
		{name: "prerelease", tag: "v2.0.0-rc.1", before: zero, created: true},
		{name: "build metadata", tag: "v2.0.0+build.7", before: zero, created: true},
		{name: "missing patch", tag: "v1.2", before: zero, created: true, reason: releaseTagReasonInvalid},
		{name: "missing v prefix", tag: "1.2.3", before: zero, created: true, reason: releaseTagReasonInvalid},
		{name: "leading zero", tag: "v01.2.3", before: zero, created: true, reason: releaseTagReasonInvalid},
		{name: "moved tag", tag: "v1.2.3", before: "0123456789abcdef0123456789abcdef01234567", reason: releaseTagReasonMoved},
		{name: "missing created flag", tag: "v1.2.3", before: zero, reason: releaseTagReasonMoved},
		{name: "forced tag", tag: "v1.2.3", before: zero, created: true, forced: true, reason: releaseTagReasonMoved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReleaseTag(tt.tag, tt.before, tt.created, tt.forced)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("validateReleaseTag() = %v, want nil", err)
				}
				return
			}
			if !isReleaseTagRejected(err) || releaseTagRejectReason(err) != tt.reason {
				t.Fatalf("validateReleaseTag() = %v, want rejection %q", err, tt.reason)
			}
		})
	}
}

func TestHandlePushRequest_MovedTagIsIgnoredBeforeBindingLookup(t *testing.T) {
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// A moved tag must be rejected before any binding or source dependency
	// is touched. Leaving those dependencies unset makes that ordering
	// observable: the result must still be the typed policy rejection.
	body := []byte(`{"ref":"refs/tags/v1.2.3","before":"0123456789abcdef0123456789abcdef01234567","after":"fedcba98","created":false,"forced":true,"repository":{"full_name":"octo/api","name":"api","default_branch":"main"},"installation":{"id":42}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !isReleaseTagRejected(err) || releaseTagRejectReason(err) != releaseTagReasonMoved {
		t.Fatalf("HandlePushRequest() = %v, want moved-tag rejection", err)
	}
}

func TestHandlePushRequest_BranchDeletionIsIgnored(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	body := []byte(`{"ref":"refs/heads/main","after":"0000000000000000000000000000000000000000","deleted":true,"repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !IsNoBinding(err) {
		t.Errorf("branch deletion → err = %v, want ErrNoBinding", err)
	}
}

func TestWebhookHTTPHandler_IsLoopbackOnly(t *testing.T) {
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	svc.WebhookHTTPHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("direct handler status = %d, want 501", rr.Code)
	}
}

// recordingEnqueuer is the BuildEnqueuer stub for PR-GH.5
// fan-out tests. It records every call and returns the
// configured buildID (or error).
//
// Issue #432 phase 5: the seam grew from (accountID, appID,
// commitSHA) string triples to BuildSpec structs (carrying the
// staged source path). The stub records the buildID-mint
// inputs and the BuildSpec so the fan-out tests can assert
// on the per-app source path that the staging step produced.
type recordingEnqueuer struct {
	buildID string
	err     error
	calls   []enqueueCall
}

type enqueueCall struct {
	accountID  string
	appID      string
	deliveryID string
	commitSHA  string
	sourcePath string
}

func (r *recordingEnqueuer) Enqueue(_ context.Context, spec BuildSpec) (state.Build, error) {
	r.calls = append(r.calls, enqueueCall{
		accountID:  spec.App.AccountID,
		appID:      spec.App.ID,
		deliveryID: spec.DeliveryID,
		commitSHA:  spec.CommitSHA,
		sourcePath: spec.SourcePath,
	})
	if r.err != nil {
		return state.Build{}, r.err
	}
	return state.Build{ID: r.buildID, Kind: state.DeploymentKindGitHub}, nil
}

// TestHandlePushRequest_FanOut_HappyPath is the PR-GH.5
// gate. The stub Scan returns one Tier-1 workload, so the
// reconcile populates Result.Added with one app. The fan-out
// must enqueue exactly one build with the right (accountID,
// appID, commitSHA) triple.
func TestHandlePushRequest_FanOut_HappyPath(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	enq := &recordingEnqueuer{buildID: "build-1"}
	svc.Enqueuer = enq
	body := []byte(`{"ref":"refs/heads/main","after":"sha-1","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)

	result, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(enq.calls) != 1 {
		t.Fatalf("enqueue calls = %d, want 1", len(enq.calls))
	}
	if len(result.BuildIDs) != 1 || result.BuildIDs[0] != "build-1" {
		t.Errorf("result.BuildIDs = %v, want [build-1]", result.BuildIDs)
	}
	if enq.calls[0].commitSHA != "sha-1" {
		t.Errorf("enqueue commitSHA = %q, want sha-1", enq.calls[0].commitSHA)
	}
	if enq.calls[0].accountID != rig.acct {
		t.Errorf("enqueue accountID = %q, want %q", enq.calls[0].accountID, rig.acct)
	}
}

// TestHandlePushRequest_FanOut_EmptyBuilds_NoEnqueue:
// the reconcile returns no Added/Changed (nothing to deploy)
// so the fan-out is a no-op. The HTTP response carries
// build_ids: [].
func TestHandlePushRequest_FanOut_EmptyBuilds_NoEnqueue(t *testing.T) {
	// Empty scan triggers the never-empty alert via
	// Result.Alerts — no app rows added. The fan-out sees
	// an empty Added+Changed list and does not enqueue.
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) {
		return reposcan.Result{Workloads: nil, Managed: nil, Tier: 1}, nil
	})
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	enq := &recordingEnqueuer{buildID: "build-1"}
	svc.Enqueuer = enq
	body := []byte(`{"ref":"refs/heads/main","after":"sha-1","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)

	result, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(enq.calls) != 0 {
		t.Errorf("enqueue calls = %d, want 0 (no apps touched)", len(enq.calls))
	}
	if len(result.BuildIDs) != 0 {
		t.Errorf("result.BuildIDs = %v, want []", result.BuildIDs)
	}
}

// TestHandlePushRequest_FanOut_EnqueueError_SoftFail drives
// the partial-success path. The reconcile returns one
// added app, the enqueuer returns an error. The push is
// still successful (200 OK with empty build_ids); the
// error is logged but not propagated. This is the
// "best-effort fan-out" contract: failing the whole push
// because one of N builds was rejected is worse for the
// customer than logging + continuing.
func TestHandlePushRequest_FanOut_EnqueueError_SoftFail(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	enq := &recordingEnqueuer{err: errors.New("queue full")}
	svc.Enqueuer = enq
	body := []byte(`{"ref":"refs/heads/main","after":"sha-1","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)

	result, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest returned error: %v (must be soft-fail)", err)
	}
	if len(result.BuildIDs) != 0 {
		t.Errorf("result.BuildIDs = %v, want [] (enqueue failed)", result.BuildIDs)
	}
}

func TestHandlePushRequest_DurableDeliveryRetriesPartialFanOut(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	enq := &recordingEnqueuer{err: errors.New("queue unavailable")}
	svc.Enqueuer = enq
	body := []byte(`{"ref":"refs/heads/main","after":"sha-1","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	ctx := context.WithValue(context.Background(), webhookDeliveryContextKey{}, "delivery-1")

	_, err := svc.HandlePushRequest(ctx, body)
	if err == nil || !strings.Contains(err.Error(), "delivery-1 incomplete") {
		t.Fatalf("HandlePushRequest error = %v, want durable delivery retry error", err)
	}
	if len(enq.calls) != 1 || enq.calls[0].deliveryID != "delivery-1" {
		t.Fatalf("enqueue calls = %+v, want propagated delivery id", enq.calls)
	}
}

// TestNoopEnqueuer_UniqueIDs pins the noop enqueuer's
// uniqueness contract. UUIDv7 means two successive calls
// produce different IDs; the "noop-build-" prefix lets
// dashboards filter fake IDs out of real-build metrics.
func TestNoopEnqueuer_UniqueIDs(t *testing.T) {
	e := NewNoopEnqueuer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	spec := BuildSpec{
		App:       state.App{ID: "app-1", AccountID: "acct-1"},
		CommitSHA: "sha-1",
	}
	b1, err := e.Enqueue(context.Background(), spec)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	b2, err := e.Enqueue(context.Background(), spec)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if b1.ID == b2.ID {
		t.Errorf("b1.ID == b2.ID (%q); repeat calls must produce different UUIDs", b1.ID)
	}
	if !strings.HasPrefix(b1.ID, "noop-build-") {
		t.Errorf("b1.ID = %q; want noop-build- prefix", b1.ID)
	}
	if !strings.HasPrefix(b2.ID, "noop-build-") {
		t.Errorf("b2.ID = %q; want noop-build- prefix", b2.ID)
	}
}

// TestHandlePushRequest_BindingInstallIDMismatch_ErrNoBinding
// pins the M8 takeover guard. A bind row whose InstallID
// diverges from the install row's InstallationID is a stale
// binding (rotated webhook secret, takeover/rebind flow).
// The push must NOT dispatch to the wrong repo; the
// service returns ErrNoBinding so the webhook handler
// renders the 200-ignored body.
func TestHandlePushRequest_BindingInstallIDMismatch_ErrNoBinding(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Binding InstallID=42; install row's
	// InstallationID=99. They must diverge so the guard
	// fires.
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{
		"octo/api|main": {BindingID: "b-1", AccountID: rig.acct, InstallID: 42, RepoFullName: "octo/api", ProductionBranch: "main"},
	}}
	svc.Installs = &stubInstalls{byAccount: map[string]state.GitHubInstall{
		rig.acct: {AccountID: rig.acct, InstallationID: 99, DefaultBranch: "main"},
	}}
	svc.Source = &stubSource{fsys: fstest.MapFS{}}
	svc.Reconcile = rig.rec
	body := []byte(`{"ref":"refs/heads/main","after":"x","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !IsNoBinding(err) {
		t.Errorf("err = %v, want ErrNoBinding on InstallID mismatch", err)
	}
}

// TestHandlePushRequest_TreeCloseNilSafe pins H6. A Source
// error returns early without a dangling tree reference.
// The production wiring does NOT defer tree.Close before
// the err-check — moving the defer into the success branch
// means a nil tree is never dereferenced. We don't have a
// way to inject a nil-but-no-error Source without breaking
// the SourceFetcher contract, so this test pins the err-path
// behavior: a Source error short-circuits without panic.
func TestHandlePushRequest_TreeCloseNilSafe(t *testing.T) {
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	want := errors.New("codeload down")
	svc.Source = &stubSource{err: want}
	body := []byte(`{"ref":"refs/heads/main","after":"x","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	// Should not panic. The error message is wrapped with
	// "githubd: source fetch".
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "githubd: source fetch") {
		t.Errorf("err = %v, want op prefix 'githubd: source fetch'", err)
	}
}

// stubChangedFiles adapts a (files, err) pair to the
// ChangedFilesClient interface so HandlePushRequest's path-
// filter path can be exercised without spinning up an httptest
// server. records last args for spy assertions.
type stubChangedFiles struct {
	files []string
	err   error

	calls      int
	lastInstID int64
	lastOwner  string
	lastRepo   string
	lastBase   string
	lastHead   string
}

func (s *stubChangedFiles) ChangedFiles(_ context.Context, instID int64, owner, repo, base, head string) ([]string, error) {
	s.calls++
	s.lastInstID = instID
	s.lastOwner = owner
	s.lastRepo = repo
	s.lastBase = base
	s.lastHead = head
	return s.files, s.err
}

// pathFilterRig builds a Service with a recordingEnqueuer and an
// optional ChangedFilesClient. Used by the path-filter tests
// below. Returns (service, recorder, acctID).
//
// includeRootWorkload toggles whether the stub Scan produces a
// third repo-root workload (RootDir == "") in addition to the
// two member dirs. Tests that want to exercise the lockfile
// fallback (rebuild all on no match) set includeRootWorkload=false
// so a non-matching change genuinely matches no one.
//
// The stub Scan returns a Tier-3 (workspaces) result with each
// workload's Source field tagged "workspaces:<name>" so
// reconcile.DeriveScanSource returns ProjectScanSourceWorkspace —
// matching the seeded project. The scan-source stability guard
// trips if the project's stored source is below the desired tier;
// we seed at the same tier to exercise the happy path.
func pathFilterRig(t *testing.T, cf ChangedFilesClient, includeRootWorkload bool) (*Service, *recordingEnqueuer, string) {
	t.Helper()
	workloads := []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "auth", RootDir: "services/auth/api", Source: "workspaces:auth"},
		{Class: reposcan.ClassHTTP, Name: "billing", RootDir: "services/billing", Source: "workspaces:billing"},
	}
	if includeRootWorkload {
		workloads = append(workloads, reposcan.Workload{
			Class: reposcan.ClassHTTP, Name: "root", RootDir: "", Source: "workspaces:root",
		})
	}
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) {
		return reposcan.Result{
			Workloads: workloads,
			Managed:   []reposcan.Managed{},
			Tier:      3,
		}, nil
	})
	_, err := rig.mem.CreateProject(context.Background(), state.Project{
		AccountID:        rig.acct,
		Slug:             "demo",
		InstallID:        rig.install,
		RepoFullName:     "octo/api",
		ProductionBranch: "main",
		// DeriveScanSource returns "workspaces" (plural) for the
		// tier-3 convention; the singular typed const
		// ProjectScanSourceWorkspace is a different slot. Use
		// the plural typed const so the test fails loudly if
		// either const drifts (the L1 review found the raw
		// string literal hid the tierRank asymmetry).
		ScanSource: state.ProjectScanSourceWorkspaces,
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	svc := newServiceForRig(t, rig)
	rec := &recordingEnqueuer{}
	svc.Enqueuer = rec
	svc.ChangedFiles = cf
	return svc, rec, rig.acct
}

func TestHandlePushRequest_PathFilter_MatchesOneApp(t *testing.T) {
	cf := &stubChangedFiles{files: []string{"services/auth/api/index.ts"}}
	svc, rec, _ := pathFilterRig(t, cf, true)
	body := []byte(`{"ref":"refs/heads/main","before":"base123","after":"head456","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	// Two apps match: services/auth/api (path intersects) + the
	// repo-root workload (RootDir == "" always rebuilds). Billing
	// is the only one that gets skipped.
	if len(rec.calls) != 2 {
		t.Fatalf("Enqueue calls = %d, want 2 (auth + root)", len(rec.calls))
	}
	if cf.calls != 1 {
		t.Errorf("ChangedFiles calls = %d, want 1", cf.calls)
	}
}

func TestHandlePushRequest_PathFilter_SourceOnlyRetryStillBuilds(t *testing.T) {
	cf := &stubChangedFiles{files: []string{"services/auth/api/index.ts"}}
	svc, rec, _ := pathFilterRig(t, cf, false)
	body := []byte(`{"ref":"refs/heads/main","before":"base123","after":"head456","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := svc.HandlePushRequest(context.Background(), body); err != nil {
			t.Fatalf("HandlePushRequest attempt %d: %v", attempt, err)
		}
	}
	if len(rec.calls) != 2 {
		t.Fatalf("Enqueue calls = %d, want one auth build on both initial and converged retry", len(rec.calls))
	}
}

func TestHandlePushRequest_PathFilter_RootDirEmptyAlwaysRebuilds(t *testing.T) {
	// Single repo-root workload; changed file is anywhere — should
	// rebuild because RootDir == "".
	cf := &stubChangedFiles{files: []string{"somewhere/unrelated.ts"}}
	svc, rec, _ := pathFilterRig(t, cf, true)
	body := []byte(`{"ref":"refs/heads/main","before":"b","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("Enqueue calls = %d, want 1 (root-dir workload always rebuilds)", len(rec.calls))
	}
}

func TestHandlePushRequest_PathFilter_LockfileFallbackRebuildsAll(t *testing.T) {
	// package.json at repo root, not under any member's RootDir.
	// We disable the root-dir workload in the rig so a non-matching
	// change genuinely matches no one — the spec's lockfile/CI
	// fallback then rebuilds every touched app.
	cf := &stubChangedFiles{files: []string{"package.json"}}
	svc, rec, _ := pathFilterRig(t, cf, false)
	body := []byte(`{"ref":"refs/heads/main","before":"b","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("Enqueue calls = %d, want 2 (lockfile fallback rebuilds both touched apps)", len(rec.calls))
	}
}

func TestHandlePushRequest_PathFilter_TruncatedFallsBack(t *testing.T) {
	cf := &stubChangedFiles{err: ErrTruncated}
	svc, rec, _ := pathFilterRig(t, cf, true)
	body := []byte(`{"ref":"refs/heads/main","before":"b","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(rec.calls) != 3 {
		t.Fatalf("Enqueue calls = %d, want 3 (ErrTruncated → full fan-out)", len(rec.calls))
	}
}

func TestHandlePushRequest_PathFilter_CompareErrorFallsBack(t *testing.T) {
	cf := &stubChangedFiles{err: ErrUnavailable}
	svc, rec, _ := pathFilterRig(t, cf, true)
	body := []byte(`{"ref":"refs/heads/main","before":"b","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(rec.calls) != 3 {
		t.Fatalf("Enqueue calls = %d, want 3 (any compare error → full fan-out)", len(rec.calls))
	}
}

func TestHandlePushRequest_PathFilter_EmptyBeforeFallsBack(t *testing.T) {
	// First push on a branch: before is empty. Service must NOT
	// call the client (can't form compare URL).
	cf := &stubChangedFiles{files: []string{"x.ts"}}
	svc, rec, _ := pathFilterRig(t, cf, true)
	body := []byte(`{"ref":"refs/heads/main","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if cf.calls != 0 {
		t.Errorf("ChangedFiles calls = %d, want 0 (empty before skips the client)", cf.calls)
	}
	if len(rec.calls) != 3 {
		t.Fatalf("Enqueue calls = %d, want 3 (empty before → full fan-out)", len(rec.calls))
	}
}

func TestHandlePushRequest_PathFilter_NilClient_StillFallsBack_ForTestOnly(t *testing.T) {
	// Back-compat for the test rig: tests that don't wire
	// ChangedFiles get the naive full fan-out (PR-GH.5
	// behaviour). Production code paths ALWAYS wire something —
	// either NewHTTPChangedFiles (credentialed box) or
	// NewUnavailableChangedFiles (credentials-missing box) — so
	// this nil-client branch is rig-only and must not be deleted
	// by future refactors thinking "production never hits it."
	svc, rec, _ := pathFilterRig(t, nil, true)
	body := []byte(`{"ref":"refs/heads/main","before":"b","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(rec.calls) != 3 {
		t.Fatalf("Enqueue calls = %d, want 3 (nil ChangedFiles → full fan-out)", len(rec.calls))
	}
}

// TestHandlePushRequest_PathFilter_UnavailableStubFallsBack pins
// the production behaviour on credentials-missing boxes: cmd/githubd
// wires NewUnavailableChangedFiles, the dispatcher queries the
// stub, gets ErrUnavailable, observes githubd_path_filter_total{
// mode="error"}, and falls back to full fan-out. The metric label
// distinguishes this case from a healthy paths-mode push and from
// the rig-only nil-client path.
func TestHandlePushRequest_PathFilter_UnavailableStubFallsBack(t *testing.T) {
	ops := wire.NewOpsMetrics("githubd-test-unavailable")
	svc, rec, _ := pathFilterRig(t, NewUnavailableChangedFiles(), true)
	svc.Ops = ops
	body := []byte(`{"ref":"refs/heads/main","before":"b","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(rec.calls) != 3 {
		t.Fatalf("Enqueue calls = %d, want 3 (unavailable stub → full fan-out)", len(rec.calls))
	}
	// The dispatcher's lookupChangedFiles path emits
	// githubd_path_filter_total{mode="error"} on ErrUnavailable;
	// verify the metric actually ticked (the dispatcher relies on
	// this to distinguish "no credentials" from "compare API
	// reachable + filter applied successfully").
	if got := testutil.ToFloat64(ops.GithubdPathFilterTotal(wire.PathFilterModeError)); got != 1 {
		t.Errorf("githubd_path_filter_total{mode=error} = %v, want 1", got)
	}
}

// TestHandlePushRequest_PathFilter_UnavailableStub_BreakerProgression
// pins the production wiring shape end-to-end (review fixup for
// PR #521). cmd/githubd wires NewBreakerChangedFiles around
// NewUnavailableChangedFiles on every credentials-missing branch,
// so after 3 pushes the breaker trips and subsequent pushes tick
// mode=breaker_open instead of mode=error. This test would have
// caught both bugs the review surfaced:
//  1. Ops wired nil on credentials-missing boxes (silent metric
//     drop — the breaker progression never reached /metrics).
//  2. The unavailable stub wasn't wrapped in the breaker (every
//     push ticks mode=error forever, breaker_open unreachable).
//
// The breaker is shared across all 4 iterations (it's the
// production wiring shape — NewBreakerChangedFiles lives for
// the daemon's lifetime). A fresh Service + OpsMetrics +
// project seed is built per iteration because once reconcile
// settles a project, subsequent pushes see Added=∅, Changed=∅
// and the dispatcher's `len(touched) > 0` short-circuit bypasses
// lookupChangedFiles entirely — which would tick mode=paths
// instead of routing through the breaker.
func TestHandlePushRequest_PathFilter_UnavailableStub_BreakerProgression(t *testing.T) {
	ops := wire.NewOpsMetrics("githubd-test-breaker-progression")
	// Shared breaker across all 4 pushes — this is the production
	// wiring shape (NewBreakerChangedFiles wraps the unavailable
	// stub and lives for the daemon's lifetime).
	breaker := NewBreakerChangedFiles(NewUnavailableChangedFiles(), time.Now)
	rec := &recordingEnqueuer{}

	for i := 0; i < 4; i++ {
		svc, _, _ := pathFilterRig(t, nil, true)
		svc.ChangedFiles = breaker
		svc.Ops = ops
		svc.Enqueuer = rec
		body := []byte(`{"ref":"refs/heads/main","before":"b","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
		if _, err := svc.HandlePushRequest(context.Background(), body); err != nil {
			t.Fatalf("push %d: HandlePushRequest: %v", i+1, err)
		}
	}

	// Each push rebuilds 3 apps via full fan-out (the rig always
	// produces Added=3 because the project is fresh per iteration).
	// Total = 4 pushes × 3 apps = 12 enqueue calls.
	if got := len(rec.calls); got != 12 {
		t.Errorf("Enqueue calls = %d, want 12 (4 pushes × 3 apps)", got)
	}
	// First 3 pushes tick mode=error; the 4th hits the open
	// breaker and ticks mode=breaker_open. This is the load-bearing
	// signal the FaasGithubdPathFilterDegraded alert + runbook
	// claim to surface.
	if got := testutil.ToFloat64(ops.GithubdPathFilterTotal(wire.PathFilterModeError)); got != 3 {
		t.Errorf("githubd_path_filter_total{mode=error} = %v, want 3", got)
	}
	if got := testutil.ToFloat64(ops.GithubdPathFilterTotal(wire.PathFilterModeBreakerOpen)); got != 1 {
		t.Errorf("githubd_path_filter_total{mode=breaker_open} = %v, want 1", got)
	}
	// Defensive: no healthy ticks — we're stubbing, not the real
	// path-filter mode. A mode=paths increment would indicate
	// the stub is leaking or the dispatcher's mode decision has
	// regressed to "filter applied successfully."
	if got := testutil.ToFloat64(ops.GithubdPathFilterTotal(wire.PathFilterModePaths)); got != 0 {
		t.Errorf("githubd_path_filter_total{mode=paths} = %v, want 0", got)
	}
}

func TestHandlePushRequest_PathFilter_PrefixCollisionDoesNotMatch(t *testing.T) {
	// services/billing must NOT match services/billing-x.ts. The
	// rig includes a repo-root workload (RootDir="") that always
	// rebuilds, so the result is exactly 1 enqueue (root alone) —
	// NOT 3 (no lockfile fallback because root matched).
	cf := &stubChangedFiles{files: []string{"services/billing-x/index.ts"}}
	svc, rec, _ := pathFilterRig(t, cf, true)
	body := []byte(`{"ref":"refs/heads/main","before":"b","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("Enqueue calls = %d, want 1 (root-dir workload alone; prefix collision prevents billing from matching)", len(rec.calls))
	}
}

// TestHandlePushRequest_PathFilter_NoTouchedApps_SkipsCompare pins
// review finding #1: when reconcile produces zero touched apps
// (the binding exists but the scan returned no workloads and no
// existing apps were drifted), the push-dispatch path must skip
// the GitHub compare-API call entirely. Per-installation API
// quota is too precious to burn on no-op webhook deliveries.
//
// The empty-touched set is constructed via a dedicated rig whose
// scan stub returns a Tier-3 result with zero workloads (and no
// existing apps on the project).
func TestHandlePushRequest_PathFilter_NoTouchedApps_SkipsCompare(t *testing.T) {
	cf := &stubChangedFiles{files: []string{"x.ts"}}
	// rig with an empty workloads slice -> reconcile produces no
	// Added, no Changed -> touched is empty -> lookupChangedFiles
	// must NOT be called.
	rig := newRig(t, func(_ fs.FS) (reposcan.Result, error) {
		return reposcan.Result{Workloads: nil, Managed: nil, Tier: 3}, nil
	})
	_, err := rig.mem.CreateProject(context.Background(), state.Project{
		AccountID:        rig.acct,
		Slug:             "demo",
		InstallID:        rig.install,
		RepoFullName:     "octo/api",
		ProductionBranch: "main",
		ScanSource:       state.ProjectScanSourceWorkspaces,
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	svc := newServiceForRig(t, rig)
	rec := &recordingEnqueuer{}
	svc.Enqueuer = rec
	svc.ChangedFiles = cf

	body := []byte(`{"ref":"refs/heads/main","before":"b","after":"h","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err = svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	// Critical: ChangedFiles was NOT called because touched is
	// empty. The stub also has a synthetic non-empty files slice
	// to ensure the assertion is meaningful.
	if cf.calls != 0 {
		t.Errorf("ChangedFiles calls = %d, want 0 (empty touched set must skip compare-API)", cf.calls)
	}
	if len(rec.calls) != 0 {
		t.Errorf("Enqueue calls = %d, want 0 (nothing to enqueue)", len(rec.calls))
	}
}

// TestPathIntersectsDir pins the table directly. The behaviour is
// load-bearing for the prefix-collision guard above.
func TestPathIntersectsDir(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files []string
		dir   string
		want  bool
	}{
		{"file under dir", []string{"a/b/c.ts"}, "a/b", true},
		{"file equal to dir", []string{"a/b"}, "a/b", true},
		{"file at sibling", []string{"a/c.ts"}, "a/b", false},
		{"prefix collision (auth vs auth-api)", []string{"a/auth-api/x.ts"}, "a/auth", false},
		{"file at root, dir at root", []string{"x.ts"}, "", false}, // dir=="" is filtered before this helper
		{"empty files", nil, "a/b", false},
		// GitHub emits a trailing-slash entry for directory-only
		// changes (rename of the directory itself, add/remove of
		// a directory). Without the f == dir + "/" branch, a
		// workload whose RootDir matches the renamed directory
		// would be silently skipped.
		{"directory-only entry with trailing slash", []string{"a/b/"}, "a/b", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := pathIntersectsDir(tc.files, tc.dir)
			if got != tc.want {
				t.Errorf("pathIntersectsDir(%v, %q) = %v, want %v", tc.files, tc.dir, got, tc.want)
			}
		})
	}
}

// TestSplitOwnerRepo pins the helper that decodes "owner/name"
// from GitHub's repository.full_name. Defends against the
// "owner/repo/extra" misparse and the "/repo" / "owner/" edge
// cases that would silently break the compare URL.
func TestSplitOwnerRepo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in           string
		wantOk       bool
		wantO, wantR string
	}{
		{"octo/api", true, "octo", "api"},
		{"a/b", true, "a", "b"},
		{"a/b/c", false, "", ""}, // too many slashes
		{"", false, "", ""},
		{"/repo", false, "", ""},  // empty owner
		{"owner/", false, "", ""}, // empty repo
		{"single", false, "", ""}, // no slash
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			o, r, ok := splitOwnerRepo(tc.in)
			if ok != tc.wantOk || o != tc.wantO || r != tc.wantR {
				t.Errorf("splitOwnerRepo(%q) = (%q,%q,%v), want (%q,%q,%v)",
					tc.in, o, r, ok, tc.wantO, tc.wantR, tc.wantOk)
			}
		})
	}
}

// TestFilterByPath_Unit pins the helper directly (no HTTP).
func TestFilterByPath_Unit(t *testing.T) {
	t.Parallel()
	appsWithRoot := []state.App{
		{ID: "auth", RootDir: "services/auth/api"},
		{ID: "billing", RootDir: "services/billing"},
		{ID: "root", RootDir: ""},
	}
	appsNoRoot := []state.App{
		{ID: "auth", RootDir: "services/auth/api"},
		{ID: "billing", RootDir: "services/billing"},
	}
	cases := []struct {
		name      string
		apps      []state.App
		files     []string
		mode      string
		wantIDs   []string
		wantSkips []string
	}{
		{
			name:      "paths mode + root_dir match",
			apps:      appsWithRoot,
			files:     []string{"services/auth/api/index.ts"},
			mode:      "paths",
			wantIDs:   []string{"auth", "root"},
			wantSkips: []string{"billing"},
		},
		{
			// RootDir == "" always matches, so anyMatched=true and
			// we don't fall through to the lockfile-rebuild-all
			// branch — only `root` (the repo-root workload) is in
			// matched. The lockfile-fallback path is only exercised
			// when NO member's RootDir is touched AND no member has
			// RootDir == "".
			name:      "paths mode + lockfile + root-dir workload rebuilds alone",
			apps:      appsWithRoot,
			files:     []string{"package.json"},
			mode:      "paths",
			wantIDs:   []string{"root"},
			wantSkips: []string{"auth", "billing"},
		},
		{
			// Lockfile fallback (no member touched, no root-dir
			// workload present): rebuild everything.
			name:    "paths mode + lockfile fallback (no root-dir workload)",
			apps:    appsNoRoot,
			files:   []string{"package.json"},
			mode:    "paths",
			wantIDs: []string{"auth", "billing"},
		},
		{
			name:    "full_fallback mode is identity",
			apps:    appsWithRoot,
			files:   nil, // ignored in full_fallback
			mode:    "full_fallback",
			wantIDs: []string{"auth", "billing", "root"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// filterByPath is a method on Service; build a zero-value one.
			svc := &Service{}
			matched, skipped := svc.filterByPath(tc.apps, tc.files, tc.mode)
			gotIDs := make([]string, 0, len(matched))
			for _, a := range matched {
				gotIDs = append(gotIDs, a.ID)
			}
			if !stringSliceEq(gotIDs, tc.wantIDs) {
				t.Errorf("matched = %v, want %v", gotIDs, tc.wantIDs)
			}
			if !stringSliceEq(skipped, tc.wantSkips) {
				t.Errorf("skipped = %v, want %v", skipped, tc.wantSkips)
			}
		})
	}
}
