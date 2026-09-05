// handlePullRequest tests (issue #272 / ADR-094 PR-preview
// environments). Mirrors the table-driven style of
// service_test.go's TestHandlePushRequest_* but covers the
// preview-specific decision tree:
//
//   - opened/synchronize/reopened: provision (or reuse) the
//     preview apps row + write the queued Check Run.
//   - closed: stamp preview_pr_state='closed' (the janitor
//     in PR-C owns the actual teardown).
//   - fork PR (head.repo differs from base.repo): refuse +
//     neutral Check Run, NO apps row created.
//   - quota exhausted: refuse + failure Check Run with
//     upgrade hint, NO apps row created.
//   - idempotent synchronize (2nd event for the same PR):
//     reuse the existing row (state.ErrConflict → success
//     path), don't crash, write the Check Run.
//   - no binding: ErrNoBinding sentinel (the HTTP handler
//     renders 200-ignored).
//
// The test rig seeds an account + install + project + parent
// app and wires a recording WritePreviewCheck /
// WritePreviewCheckForkRefused so the assertions can pin the
// exact outbound calls.
package githubd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewTestRig extends testRig with the PR-preview-specific
// fixtures: a binding keyed on (repo,) (no branch), the
// parent app bound to the project's app_id, and recording
// WritePreviewCheck / WritePreviewCheckForkRefused seams.
type previewTestRig struct {
	*testRig
	parentSlug string
	parentID   string
}

func newPreviewRig(t *testing.T) *previewTestRig {
	t.Helper()
	rig := newRig(t, nil)
	// Parent app — slug "demo-app", bound to the project's
	// installation. The handler resolves parentApp via
	// Reoncile.Store.AppByID(binding.AppID), so the binding
	// row's AppID must match the parent's UUID.
	parent, err := rig.mem.CreateApp(context.Background(), state.App{
		AccountID:      rig.acct,
		Slug:           "demo-app",
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
		IdleTimeoutS:   30,
		Status:         state.AppActive,
	})
	if err != nil {
		t.Fatalf("seed parent app: %v", err)
	}
	// Project with AppID set so the preview handler can pick
	// up the parent via Reoncile.Store.ProjectByRepo... wait,
	// the handler resolves via binding.AppID directly (the
	// bind row carries the parent app). The project is just
	// a sentinel for the binding adapter; the handler doesn't
	// call ProjectByRepo. We seed a minimal project for
	// shape-completeness but it's unused.
	_ = rig
	return &previewTestRig{
		testRig:    rig,
		parentSlug: "demo-app",
		parentID:   parent.ID,
	}
}

// newPreviewService wires a Service with the PR-preview test
// fakes: a binding keyed on (octo/api,) pointing at the rig's
// install + the parent app, plus recording WritePreviewCheck
// seams. Returns the service + the recording sinks so the
// caller can assert on the outbound calls.
func newPreviewService(t *testing.T, rig *previewTestRig) (*Service, *previewRecorder) {
	t.Helper()
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{
		// Branch-agnostic binding — the handler passes ""
		// for branch, so the key is "octo/api|".
		"octo/api|": {
			BindingID:        "b-pr-1",
			AccountID:        rig.acct,
			InstallID:        rig.install,
			AppID:            rig.parentID,
			RepoFullName:     "octo/api",
			ProductionBranch: "main",
		},
	}}
	svc.Installs = &stubInstalls{byAccount: map[string]state.GitHubInstall{
		rig.acct: {AccountID: rig.acct, InstallationID: rig.install, DefaultBranch: "main"},
	}}
	svc.Reconcile = rig.rec
	rec := &previewRecorder{}
	svc.WritePreviewCheck = rec.writeCheck
	svc.WritePreviewCheckForkRefused = rec.writeForkRefused
	svc.WritePreviewDestroyComment = rec.writeDestroyComment
	return svc, rec
}

// previewRecorder captures the (repo, sha, phase, previewURL,
// summary) tuples the handler writes back to GitHub. The
// fork-refused path records separately so the test can assert
// the two paths independently. The destroy-comment path records
// separately too so the close-arm test can pin the (repo,
// pr_number, body) tuple without conflating with the check-run
// calls.
type previewRecorder struct {
	checks   []recordedCheck
	forks    []recordedFork
	destroys []recordedDestroyComment
}

type recordedCheck struct {
	repo, sha  string
	phase      githubdgrpc.CheckPhase
	previewURL string
	summary    string
}

type recordedFork struct {
	repo, sha, summary string
}

type recordedDestroyComment struct {
	repo, body string
	prNumber   int
}

func (p *previewRecorder) writeCheck(_ context.Context, repo, sha string, phase githubdgrpc.CheckPhase, previewURL, summary string) error {
	p.checks = append(p.checks, recordedCheck{repo: repo, sha: sha, phase: phase, previewURL: previewURL, summary: summary})
	return nil
}

func (p *previewRecorder) writeForkRefused(_ context.Context, repo, sha, summary string) error {
	p.forks = append(p.forks, recordedFork{repo: repo, sha: sha, summary: summary})
	return nil
}

func (p *previewRecorder) writeDestroyComment(_ context.Context, repo string, prNumber int, body string) error {
	p.destroys = append(p.destroys, recordedDestroyComment{repo: repo, body: body, prNumber: prNumber})
	return nil
}

// pullRequestOpenedBody returns a minimal but well-formed
// pull_request "opened" body for the given PR number + head
// SHA. head_repo.full_name == repository.full_name (no fork).
// The head_ref + head.repo.full_name fields are required by
// DecodePullRequest's IsFork helper.
func pullRequestOpenedBody(prNumber int, headSHA string) []byte {
	return []byte(`{
		"action": "opened",
		"number": ` + itoa(prNumber) + `,
		"pull_request": {
			"state": "open",
			"head_sha": "` + headSHA + `",
			"head_ref": "feature/x",
			"head": {
				"ref": "feature/x",
				"sha": "` + headSHA + `",
				"repo": {"full_name": "octo/api"}
			}
		},
		"repository": {"full_name": "octo/api", "name": "api"},
		"installation": {"id": 42},
		"sender": {"login": "alice"}
	}`)
}

func pullRequestSyncBody(prNumber int, headSHA string) []byte {
	return []byte(`{
		"action": "synchronize",
		"number": ` + itoa(prNumber) + `,
		"pull_request": {
			"state": "open",
			"head_sha": "` + headSHA + `",
			"head_ref": "feature/x",
			"head": {"ref": "feature/x", "sha": "` + headSHA + `", "repo": {"full_name": "octo/api"}}
		},
		"repository": {"full_name": "octo/api", "name": "api"},
		"installation": {"id": 42},
		"sender": {"login": "alice"}
	}`)
}

func pullRequestClosedBody(prNumber int, headSHA string) []byte {
	return []byte(`{
		"action": "closed",
		"number": ` + itoa(prNumber) + `,
		"pull_request": {
			"state": "closed",
			"head_sha": "` + headSHA + `",
			"head_ref": "feature/x",
			"head": {"ref": "feature/x", "sha": "` + headSHA + `", "repo": {"full_name": "octo/api"}}
		},
		"repository": {"full_name": "octo/api", "name": "api"},
		"installation": {"id": 42},
		"sender": {"login": "alice"}
	}`)
}

// pullRequestReopenedBody is the mirror of pullRequestClosedBody
// for the reopened action. State field flips back to "open" so
// any consumer of the raw JSON (not the decoded action) sees the
// canonical open-shape; the decoder uses action, not state, to
// route (see PullRequestAction enum in event.go). Added with
// ADR-095 PR-C: the reopen event must clear a prior closed-state
// label on the preview row, which the dispatcher now stamps via
// SetPreviewPrState. Without a helper this case couldn't be
// exercised — pullRequestOpenedBody's "action":"opened" routes
// the same way but tests want to assert the closed-then-reopened
// transition specifically.
func pullRequestReopenedBody(prNumber int, headSHA string) []byte {
	return []byte(`{
		"action": "reopened",
		"number": ` + itoa(prNumber) + `,
		"pull_request": {
			"state": "open",
			"head_sha": "` + headSHA + `",
			"head_ref": "feature/x",
			"head": {"ref": "feature/x", "sha": "` + headSHA + `", "repo": {"full_name": "octo/api"}}
		},
		"repository": {"full_name": "octo/api", "name": "api"},
		"installation": {"id": 42},
		"sender": {"login": "alice"}
	}`)
}

func pullRequestForkBody(prNumber int, headSHA string) []byte {
	// head.repo.full_name differs from repository.full_name →
	// IsFork returns true.
	return []byte(`{
		"action": "opened",
		"number": ` + itoa(prNumber) + `,
		"pull_request": {
			"state": "open",
			"head_sha": "` + headSHA + `",
			"head_ref": "feature/y",
			"head": {"ref": "feature/y", "sha": "` + headSHA + `", "repo": {"full_name": "evil/fork"}}
		},
		"repository": {"full_name": "octo/api", "name": "api"},
		"installation": {"id": 42},
		"sender": {"login": "alice"}
	}`)
}

// itoa is a local integer-to-string helper to avoid pulling
// strconv into the test file's top-level imports (the body
// templates are constructed at test setup time).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// TestHandlePullRequest_HappyPath_Opened covers the canonical
// first-PR-opened path: the handler resolves the binding,
// provisions a preview apps row at slug "pr-42-demo-app",
// stamps preview_pr_state='open' + preview_expires_at = now+7d,
// and writes a queued Check Run with the preview URL.
func TestHandlePullRequest_HappyPath_Opened(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)

	body := pullRequestOpenedBody(42, "deadbeef00000000000000000000000000000000")
	result, err := svc.handlePullRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("handlePullRequest: %v", err)
	}
	if result.WasIgnored {
		t.Errorf("WasIgnored = true on opened happy path; want false")
	}
	if len(result.Added) != 1 {
		t.Fatalf("result.Added = %d, want 1", len(result.Added))
	}
	got := result.Added[0]
	if got.Slug != "pr-42-demo-app" {
		t.Errorf("slug = %q, want pr-42-demo-app", got.Slug)
	}
	if got.PreviewOfSlug != "demo-app" {
		t.Errorf("PreviewOfSlug = %q, want demo-app", got.PreviewOfSlug)
	}
	if got.PreviewPrNumber != 42 {
		t.Errorf("PreviewPrNumber = %d, want 42", got.PreviewPrNumber)
	}
	if got.PreviewPrState != state.PreviewPrStateOpen {
		t.Errorf("PreviewPrState = %q, want %q", got.PreviewPrState, state.PreviewPrStateOpen)
	}
	if got.PreviewExpiresAt == nil {
		t.Errorf("PreviewExpiresAt = nil, want non-nil")
	}
	if got.Status != state.AppActive {
		t.Errorf("Status = %q, want %q", got.Status, state.AppActive)
	}

	// Verify the durable row exists in the memstore with the
	// same preview metadata (proves CreateAppIfUnderQuota wrote
	// it, not just returned the in-memory shape).
	stored, err := rig.mem.AppByID(context.Background(), got.ID)
	if err != nil {
		t.Fatalf("AppByID after provision: %v", err)
	}
	if stored.PreviewOfSlug != "demo-app" || stored.PreviewPrNumber != 42 {
		t.Errorf("stored preview metadata = (%q, %d); want (demo-app, 42)",
			stored.PreviewOfSlug, stored.PreviewPrNumber)
	}

	// Check Run assertions: queued phase, preview URL, sha.
	if len(rec.checks) != 1 {
		t.Fatalf("rec.checks = %d, want 1", len(rec.checks))
	}
	c := rec.checks[0]
	if c.phase != githubdgrpc.CheckPhaseQueued {
		t.Errorf("phase = %v, want queued", c.phase)
	}
	if c.repo != "octo/api" || c.sha != "deadbeef00000000000000000000000000000000" {
		t.Errorf("WritePreviewCheck args = (%q, %q)", c.repo, c.sha)
	}
	if c.previewURL != "https://pr-42-demo-app.gregale.dev" {
		t.Errorf("previewURL = %q, want https://pr-42-demo-app.gregale.dev", c.previewURL)
	}
	if !strings.Contains(c.summary, "PR #42") || !strings.Contains(c.summary, "demo-app") {
		t.Errorf("summary = %q, want it to mention PR #42 + demo-app", c.summary)
	}
}

// TestHandlePullRequest_Synchronize_SamePR covers the
// idempotent path: a 2nd event for the same PR reuses the
// existing apps row (state.ErrConflict → handled as success)
// and stamps a fresh Check Run. No new row should be created.
func TestHandlePullRequest_Synchronize_SamePR(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)

	// First event: opened.
	if _, err := svc.handlePullRequest(context.Background(),
		pullRequestOpenedBody(42, "deadbeef00000000000000000000000000000000")); err != nil {
		t.Fatalf("first event: %v", err)
	}

	// Second event: synchronize with a new SHA. Memstore's
	// CreateAppIfUnderQuota will surface state.ErrConflict on
	// the (account_id, slug) UNIQUE; the handler treats that
	// as idempotent success.
	if _, err := svc.handlePullRequest(context.Background(),
		pullRequestSyncBody(42, "f00dface00000000000000000000000000000000")); err != nil {
		t.Fatalf("second event (sync): %v", err)
	}

	// The preview apps table should still have exactly ONE
	// row at pr-42-demo-app — no duplicates from the second
	// event.
	previews, err := rig.mem.PreviewAppsByParent(context.Background(), rig.acct, "demo-app")
	if err != nil {
		t.Fatalf("PreviewAppsByParent: %v", err)
	}
	if len(previews) != 1 {
		t.Errorf("preview rows for parent=demo-app = %d, want 1 (idempotent)", len(previews))
	}

	// Two Check Runs queued total (one per event).
	if len(rec.checks) != 2 {
		t.Errorf("rec.checks = %d, want 2", len(rec.checks))
	}
}

// TestHandlePullRequest_Closed_StampsClosedState covers the
// closed action: the handler stamps preview_pr_state='closed'
// on the existing row (PR-C's janitor owns the actual teardown
// sweep, but the dispatcher is responsible for advancing the
// label so the janitor's closed → stale → torn_down clock can
// start on time).
func TestHandlePullRequest_Closed_StampsClosedState(t *testing.T) {
	rig := newPreviewRig(t)
	svc, _ := newPreviewService(t, rig)

	// First: open.
	if _, err := svc.handlePullRequest(context.Background(),
		pullRequestOpenedBody(42, "deadbeef00000000000000000000000000000000")); err != nil {
		t.Fatalf("open: %v", err)
	}

	// Then: close. The handler stamps preview_pr_state='closed'
	// on the same row via SetPreviewPrState (ADR-095 PR-C.1).
	// The janitor in cmd/apid/preview_janitor.go owns the
	// closed → stale → torn_down transitions thereafter, but
	// the dispatcher must advance the label so the janitor's
	// grace clock starts on time. Before PR-C the conflict
	// path swallowed the label write — a closed event left
	// preview_pr_state='open' until the row hit its TTL.
	_, err := svc.handlePullRequest(context.Background(),
		pullRequestClosedBody(42, "deadbeef00000000000000000000000000000000"))
	if err != nil {
		t.Fatalf("closed: %v", err)
	}

	// After the closed event the preview row still exists
	// (janitor owns teardown) AND its preview_pr_state is now
	// 'closed' — the dispatcher's new contract.
	previews, err := rig.mem.PreviewAppsByParent(context.Background(), rig.acct, "demo-app")
	if err != nil {
		t.Fatalf("PreviewAppsByParent: %v", err)
	}
	if len(previews) != 1 {
		t.Fatalf("preview rows after closed = %d, want 1 (janitor reaps later)", len(previews))
	}
	if got := previews[0].PreviewPrState; got != state.PreviewPrStateClosed {
		t.Errorf("PreviewPrState after closed = %q, want %q", got, state.PreviewPrStateClosed)
	}

	// A subsequent 'reopened' event must clear the closed
	// label back to 'open' — the PR is live again. This is
	// the corollary the dispatcher must enforce because
	// SetPreviewPrState refuses the no-op.
	svc.handlePullRequest(context.Background(),
		pullRequestReopenedBody(42, "deadbeef00000000000000000000000000000000"))
	if err != nil {
		t.Fatalf("reopened: %v", err)
	}
	previews, err = rig.mem.PreviewAppsByParent(context.Background(), rig.acct, "demo-app")
	if err != nil {
		t.Fatalf("PreviewAppsByParent after reopened: %v", err)
	}
	if got := previews[0].PreviewPrState; got != state.PreviewPrStateOpen {
		t.Errorf("PreviewPrState after reopened = %q, want %q", got, state.PreviewPrStateOpen)
	}
}

// TestHandlePullRequest_Closed_PostsDestroyComment covers the
// new Mega-C PR-1 surface: the close-arm writes a one-time
// PR-thread destroy hint (issue #961 leaf 3). The body must
// include the dashboard's one-click destroy URL so the PR
// author can click through without navigating away from the
// thread. The first close posts the comment; the second close
// (reopen → close cycle) is suppressed by the dedupe carrier
// apps.preview_destroy_commented_at.
func TestHandlePullRequest_Closed_PostsDestroyComment(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)

	// First: open the PR so the preview row exists.
	if _, err := svc.handlePullRequest(context.Background(),
		pullRequestOpenedBody(42, "deadbeef00000000000000000000000000000000")); err != nil {
		t.Fatalf("open: %v", err)
	}

	// First close: the handler MUST write exactly one destroy
	// comment with the dashboard URL embedded.
	_, err := svc.handlePullRequest(context.Background(),
		pullRequestClosedBody(42, "deadbeef00000000000000000000000000000000"))
	if err != nil {
		t.Fatalf("closed #1: %v", err)
	}
	if got := len(rec.destroys); got != 1 {
		t.Fatalf("destroys after closed #1 = %d, want 1", got)
	}
	d := rec.destroys[0]
	if d.repo != "octo/api" {
		t.Errorf("destroy repo = %q, want octo/api", d.repo)
	}
	if d.prNumber != 42 {
		t.Errorf("destroy pr_number = %d, want 42", d.prNumber)
	}
	if !strings.Contains(d.body, "demo-app") {
		t.Errorf("destroy body = %q, want it to reference the parent slug", d.body)
	}
	if !strings.Contains(d.body, "/dashboard/apps/demo-app/preview/") {
		t.Errorf("destroy body = %q, want it to embed the dashboard destroy URL", d.body)
	}

	// Open → close → reopen → close: the second close must NOT
	// post a duplicate comment (dedupe carrier).
	if _, err := svc.handlePullRequest(context.Background(),
		pullRequestReopenedBody(42, "deadbeef00000000000000000000000000000000")); err != nil {
		t.Fatalf("reopened: %v", err)
	}
	_, err = svc.handlePullRequest(context.Background(),
		pullRequestClosedBody(42, "deadbeef00000000000000000000000000000000"))
	if err != nil {
		t.Fatalf("closed #2: %v", err)
	}
	if got := len(rec.destroys); got != 1 {
		t.Errorf("destroys after close→reopen→close = %d, want 1 (dedupe carrier must collapse the second post)", got)
	}
}

// TestHandlePullRequest_Opened_NoDestroyComment confirms the
// open-arm does NOT post a destroy hint — the comment is the
// close-arm's surface, posted only when the PR author/maintainer
// has explicitly asked for the preview to be torn down.
func TestHandlePullRequest_Opened_NoDestroyComment(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)

	if _, err := svc.handlePullRequest(context.Background(),
		pullRequestOpenedBody(42, "deadbeef00000000000000000000000000000000")); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := len(rec.destroys); got != 0 {
		t.Errorf("destroys after open = %d, want 0 (open-arm must not post a destroy comment)", got)
	}
}

// TestHandlePullRequest_ForkRefused covers D3: head.repo
// differs from base.repo. The handler short-circuits with a
// neutral Check Run, no apps row is created, and the
// returned error is ErrIgnored (so the HTTP handler renders
// 200-ignored).
func TestHandlePullRequest_ForkRefused(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)

	_, err := svc.handlePullRequest(context.Background(),
		pullRequestForkBody(42, "deadbeef00000000000000000000000000000000"))
	if !errors.Is(err, ErrIgnored) {
		t.Errorf("err = %v, want ErrIgnored", err)
	}

	// No apps row should be created.
	previews, perr := rig.mem.PreviewAppsByParent(context.Background(), rig.acct, "demo-app")
	if perr != nil {
		t.Fatalf("PreviewAppsByParent: %v", perr)
	}
	if len(previews) != 0 {
		t.Errorf("fork PR created %d preview rows; want 0", len(previews))
	}

	// The fork-refused Check Run must have been written; the
	// production WritePreviewCheck must NOT have been called
	// (fork PRs never provision an app).
	if len(rec.forks) != 1 {
		t.Errorf("rec.forks = %d, want 1", len(rec.forks))
	}
	if len(rec.checks) != 0 {
		t.Errorf("rec.checks = %d, want 0 (fork PR must not call WritePreviewCheck)", len(rec.checks))
	}
	f := rec.forks[0]
	if !strings.Contains(f.summary, "Fork PR refused") {
		t.Errorf("fork summary = %q, want it to mention 'Fork PR refused'", f.summary)
	}
}

// TestHandlePullRequest_QuotaExhausted covers the
// DeployedAppMax path. With limits.DeployedApps=0, every
// CreateAppIfUnderQuota call returns state.QuotaError; the
// handler should refuse + write a failure Check Run + return
// ErrIgnored.
func TestHandlePullRequest_QuotaExhausted(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)

	// Wrap the rig's MemStore with a quota-exhausting stub so
	// the next CreateAppIfUnderQuota trips the
	// state.QuotaError path. The remaining Store surface
	// (AppByID, etc.) falls through to the embedded MemStore.
	stub := &quotaExceededStore{MemStore: rig.mem}
	svc.Reconcile.Store = stub

	_, err := svc.handlePullRequest(context.Background(),
		pullRequestOpenedBody(42, "deadbeef00000000000000000000000000000000"))
	if !errors.Is(err, ErrIgnored) {
		t.Errorf("err = %v, want ErrIgnored (quota exhausted)", err)
	}

	// The quota-exhausted Check Run must have been written
	// with the failure phase + upgrade hint.
	if len(rec.checks) != 1 {
		t.Fatalf("rec.checks = %d, want 1", len(rec.checks))
	}
	c := rec.checks[0]
	if c.phase != githubdgrpc.CheckPhaseFailed {
		t.Errorf("phase = %v, want Failed", c.phase)
	}
	if !strings.Contains(c.summary, "deployed app limit") {
		t.Errorf("summary = %q, want it to mention the deployed app limit", c.summary)
	}

	// No fork-refused Check Run.
	if len(rec.forks) != 0 {
		t.Errorf("rec.forks = %d, want 0 on quota path", len(rec.forks))
	}
}

// TestHandlePullRequest_NoBinding covers the case where the
// repo isn't bound. The handler should return ErrNoBinding
// (HTTP handler renders 200-ignored) without writing any
// Check Runs.
func TestHandlePullRequest_NoBinding(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)

	// Override bindings to return empty for octo/api|.
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{}}

	_, err := svc.handlePullRequest(context.Background(),
		pullRequestOpenedBody(42, "deadbeef00000000000000000000000000000000"))
	if !IsNoBinding(err) {
		t.Errorf("err = %v, want ErrNoBinding", err)
	}
	if len(rec.checks) != 0 {
		t.Errorf("rec.checks = %d, want 0 on no-binding", len(rec.checks))
	}
	if len(rec.forks) != 0 {
		t.Errorf("rec.forks = %d, want 0 on no-binding", len(rec.forks))
	}
}

// TestHandlePullRequest_DecodeError covers the action-/
// field-validation path: a body with an unknown action
// surfaces a parse error and the handler returns the error
// without writing any Check Runs.
func TestHandlePullRequest_DecodeError(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)

	// Unknown action.
	body := []byte(`{
		"action": "labeled",
		"number": 42,
		"pull_request": {
			"state": "open",
			"head_sha": "deadbeef00000000000000000000000000000000",
			"head_ref": "feature/x",
			"head": {"ref": "feature/x", "sha": "deadbeef00000000000000000000000000000000", "repo": {"full_name": "octo/api"}}
		},
		"repository": {"full_name": "octo/api", "name": "api"},
		"installation": {"id": 42},
		"sender": {"login": "alice"}
	}`)
	_, err := svc.handlePullRequest(context.Background(), body)
	if err == nil {
		t.Errorf("expected error for unknown action, got nil")
	}
	if len(rec.checks) != 0 || len(rec.forks) != 0 {
		t.Errorf("expected no Check Runs on decode error, got checks=%d forks=%d",
			len(rec.checks), len(rec.forks))
	}
}

// TestPreviewSlug covers the slug derivation helper directly.
// Table-driven: PR 42 against "hello-world" → "pr-42-hello-world";
// PR 1 against "my-app" → "pr-1-my-app"; empty parent / zero
// PR number → errors.
func TestPreviewSlug(t *testing.T) {
	cases := []struct {
		name      string
		parent    string
		prNumber  int
		want      string
		wantError bool
	}{
		{"42 against hello-world", "hello-world", 42, "pr-42-hello-world", false},
		{"1 against my-app", "my-app", 1, "pr-1-my-app", false},
		{"9999 against foo", "foo", 9999, "pr-9999-foo", false},
		{"empty parent", "", 42, "", true},
		{"zero PR number", "foo", 0, "", true},
		{"negative PR number", "foo", -1, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := previewSlug(c.parent, c.prNumber)
			if c.wantError {
				if err == nil {
					t.Errorf("previewSlug(%q, %d): err = nil, want error", c.parent, c.prNumber)
				}
				return
			}
			if err != nil {
				t.Errorf("previewSlug(%q, %d): unexpected err = %v", c.parent, c.prNumber, err)
				return
			}
			if got != c.want {
				t.Errorf("previewSlug(%q, %d) = %q, want %q", c.parent, c.prNumber, got, c.want)
			}
		})
	}
}

// TestPreviewHostnameForSlug covers the URL derivation helper
// directly. Empty slug → empty URL; non-empty slug →
// "<slug>.gregale.dev".
func TestPreviewHostnameForSlug(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"pr-42-demo-app", "pr-42-demo-app.gregale.dev"},
		{"pr-1-foo", "pr-1-foo.gregale.dev"},
		{"", ""},
	}
	for _, c := range cases {
		if got := previewHostnameForSlug(c.slug); got != c.want {
			t.Errorf("previewHostnameForSlug(%q) = %q, want %q", c.slug, got, c.want)
		}
	}
}

// quotaExceededStore embeds *state.MemStore so it satisfies
// state.Store via method promotion, then overrides
// CreateAppIfUnderQuota to always return state.QuotaError.
// The handler's remaining calls (AppByID for the parent app
// lookup) fall through to the embedded MemStore.
type quotaExceededStore struct {
	*state.MemStore
}

// CreateAppIfUnderQuota always returns a state.QuotaError so
// the handler exercises the quota-exhausted branch. We
// override the embedded method here; the rest of the Store
// surface is satisfied by *state.MemStore.
func (q *quotaExceededStore) CreateAppIfUnderQuota(ctx context.Context, app state.App, limits api.Limits) (state.App, error) {
	return state.App{}, &state.QuotaError{Limit: limits.DeployedApps, Observed: limits.DeployedApps}
}

// _ ensures the unused api import survives a future edit that
// drops the explicit api.Limits{} literal in the stub.
var _ = api.Limits{}
