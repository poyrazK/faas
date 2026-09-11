package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

// memCoverageSlice4Fixture is the slice-4 analogue of memCoverageFixture:
// account + app + deployment, plus a build with provenance so the MFA /
// project / GitHub-binding / provenance surfaces can be exercised without
// re-deriving the same rows in every test.
func memCoverageSlice4Fixture(t *testing.T) (*MemStore, context.Context, Account, App, Deployment) {
	t.Helper()
	m, ctx, account, app, deployment := memCoverageFixture(t)
	build, err := m.CreateBuild(ctx, deployment.ID, DeploymentKindDockerfile, 42, "/tmp/slice4.log")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CreateBuildProvenance(ctx, BuildProvenance{BuildID: build.ID, BuildkitVer: "v0.20", RailpackVer: "v1"}); err != nil {
		t.Fatal(err)
	}
	return m, ctx, account, app, deployment
}

func TestMemStoreCoverageMFA(t *testing.T) {
	m, ctx, account, _, _ := memCoverageSlice4Fixture(t)

	// ReadMFASecret on an account that never enrolled → ErrNotFound.
	if _, err := m.ReadMFASecret(ctx, account.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read mfa before enroll = %v", err)
	}
	if _, err := m.ReadMFASecret(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read mfa missing = %v", err)
	}
	// SetMFASecret on a missing account → ErrNotFound.
	if err := m.SetMFASecret(ctx, "missing", []byte("sealed"), nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set mfa missing = %v", err)
	}
	sealed := []byte("sealed-secret")
	hashes := [][]byte{[]byte("h1"), []byte("h2")}
	if err := m.SetMFASecret(ctx, account.ID, sealed, hashes); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ReadMFASecret(ctx, account.ID); err != nil || string(got) != string(sealed) {
		t.Fatalf("read mfa after enroll = %q, %v", got, err)
	}
	// MarkMFAEnrolled clears mfa_required + stamps enrolled_at. Idempotent.
	if err := m.MarkMFAEnrolled(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	acct, err := m.AccountByID(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if acct.MFAEnrolledAt == nil || acct.MFARequired {
		t.Fatalf("enrolled account = %+v", acct)
	}
	if err := m.MarkMFAEnrolled(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mark enrolled missing = %v", err)
	}
	// SetMFARequired: changed true on a real flip, false on a no-op,
	// ErrNotFound on a missing row.
	if changed, err := m.SetMFARequired(ctx, account.ID, true); err != nil || !changed {
		t.Fatalf("set mfa required = %v, %v", changed, err)
	}
	if changed, err := m.SetMFARequired(ctx, account.ID, true); err != nil || changed {
		t.Fatalf("set mfa required no-op = %v, %v", changed, err)
	}
	if _, err := m.SetMFARequired(ctx, "missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set mfa required missing = %v", err)
	}
	// ClearMFA nulls the secret + hashes + enrolled_at, keeps mfa_required.
	if err := m.ClearMFA(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReadMFASecret(ctx, account.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read mfa after clear = %v", err)
	}
	if err := m.ClearMFA(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clear mfa missing = %v", err)
	}
}

func TestMemStoreCoverageCountDeployments(t *testing.T) {
	m, ctx, account, app, deployment := memCoverageSlice4Fixture(t)
	if n, err := m.CountDeployments(ctx, account.ID); err != nil || n != 1 {
		t.Fatalf("count deployments = %d, %v", n, err)
	}
	// A failed deployment does not count (SetDeploymentFailed is the
	// production writer for status='failed').
	if _, err := m.SetDeploymentFailed(ctx, deployment.ID, "build_failed", "boom"); err != nil {
		t.Fatal(err)
	}
	if n, err := m.CountDeployments(ctx, account.ID); err != nil || n != 0 {
		t.Fatalf("count after failed = %d, %v", n, err)
	}
	// A superseded deployment does not count either. CreateDeployment
	// flips the prior pending/live row to superseded automatically.
	if _, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:next"}); err != nil {
		t.Fatal(err)
	}
	if n, err := m.CountDeployments(ctx, account.ID); err != nil || n != 1 {
		t.Fatalf("count after supersede = %d, %v", n, err)
	}
	if n, err := m.CountDeployments(ctx, "missing"); err != nil || n != 0 {
		t.Fatalf("count missing = %d, %v", n, err)
	}
}

func TestMemStoreCoverageProjects(t *testing.T) {
	m, ctx, account, app, _ := memCoverageSlice4Fixture(t)

	// CreateProject — unknown account → ErrNotFound.
	if _, err := m.CreateProject(ctx, Project{AccountID: "missing", Slug: "p"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("create project missing account = %v", err)
	}
	proj, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "proj-one", InstallID: 77, RepoFullName: "acme/repo"})
	if err != nil {
		t.Fatal(err)
	}
	// Duplicate slug → ErrConflict.
	if _, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "proj-one"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug = %v", err)
	}
	// Duplicate (install, repo) → ErrConflict.
	if _, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "proj-two", InstallID: 77, RepoFullName: "acme/repo"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate install repo = %v", err)
	}
	// ProjectByID — hit + miss.
	if got, err := m.ProjectByID(ctx, proj.ID); err != nil || got.ID != proj.ID {
		t.Fatalf("project by id = %+v, %v", got, err)
	}
	if _, err := m.ProjectByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project by id missing = %v", err)
	}
	// ProjectBySlug — hit + miss (both empty-map and missing-slug).
	if got, err := m.ProjectBySlug(ctx, account.ID, "proj-one"); err != nil || got.ID != proj.ID {
		t.Fatalf("project by slug = %+v, %v", got, err)
	}
	if _, err := m.ProjectBySlug(ctx, account.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project by slug missing = %v", err)
	}
	if _, err := m.ProjectBySlug(ctx, "missing", "proj-one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project by slug missing account = %v", err)
	}
	// ProjectByRepo — hit + miss + cross-account filter.
	if got, err := m.ProjectByRepo(ctx, "", 77, "acme/repo"); err != nil || got.ID != proj.ID {
		t.Fatalf("project by repo = %+v, %v", got, err)
	}
	if _, err := m.ProjectByRepo(ctx, account.ID, 77, "acme/repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ProjectByRepo(ctx, "other-account", 77, "acme/repo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project by repo wrong account = %v", err)
	}
	if _, err := m.ProjectByRepo(ctx, "", 99, "acme/repo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project by repo missing = %v", err)
	}
	// ListProjectsForAccount.
	proj2, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "proj-two-b"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := m.ListProjectsForAccount(ctx, account.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list projects = %+v, %v", list, err)
	}
	if got, err := m.ListProjectsForAccount(ctx, "missing"); err != nil || len(got) != 0 {
		t.Fatalf("list projects missing = %+v, %v", got, err)
	}
	// Bind an app to the project so DeleteProject's SET NULL walk is
	// exercised.
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{Manifest: &AppManifest{}}); err != nil {
		t.Fatal(err)
	}
	// DeleteProject — hit + miss.
	if err := m.DeleteProject(ctx, proj2.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteProject(ctx, proj2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete project repeat = %v", err)
	}
}

func TestMemStoreCoverageGitHubBindings(t *testing.T) {
	m, ctx, account, app, _ := memCoverageSlice4Fixture(t)

	binding := GitHubBinding{
		AppID: app.ID, AccountID: account.ID, BindingID: "bind-1",
		InstallID: 11, RepoFullName: "acme/app", ProductionBranch: "main",
	}
	// UpsertGithubInstallBinding — empty AppID/BindingID/AccountID rejected.
	if err := m.UpsertGithubInstallBinding(ctx, GitHubBinding{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("upsert binding empty = %v", err)
	}
	if err := m.UpsertGithubInstallBinding(ctx, GitHubBinding{AppID: app.ID}); err == nil {
		t.Fatal("upsert binding no binding id should fail")
	}
	if err := m.UpsertGithubInstallBinding(ctx, GitHubBinding{AppID: app.ID, BindingID: "b"}); err == nil {
		t.Fatal("upsert binding no account should fail")
	}
	if err := m.UpsertGithubInstallBinding(ctx, GitHubBinding{AppID: "missing", BindingID: "b", AccountID: account.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("upsert binding missing app = %v", err)
	}
	if err := m.UpsertGithubInstallBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	// GetGithubInstallBindingForApp — hit + miss (empty ids / wrong account).
	if got, err := m.GetGithubInstallBindingForApp(ctx, app.ID, account.ID); err != nil || got.BindingID != "bind-1" {
		t.Fatalf("get binding = %+v, %v", got, err)
	}
	if _, err := m.GetGithubInstallBindingForApp(ctx, "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get binding empty = %v", err)
	}
	if _, err := m.GetGithubInstallBindingForApp(ctx, app.ID, "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get binding wrong account = %v", err)
	}
	// GithubInstallBindingForRepoBranch — hit + miss + default-branch.
	if got, err := m.GithubInstallBindingForRepoBranch(ctx, "acme/app", "main"); err != nil || got.BindingID != "bind-1" {
		t.Fatalf("binding by repo branch = %+v, %v", got, err)
	}
	if _, err := m.GithubInstallBindingForRepoBranch(ctx, "acme/app", "dev"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("binding by repo wrong branch = %v", err)
	}
	if _, err := m.GithubInstallBindingForRepoBranch(ctx, "", "main"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("binding by repo empty = %v", err)
	}
	// ListGithubInstallBindingsForAccount — hit + empty + empty-account.
	if got, err := m.ListGithubInstallBindingsForAccount(ctx, account.ID); err != nil || len(got) != 1 {
		t.Fatalf("list bindings = %+v, %v", got, err)
	}
	if got, err := m.ListGithubInstallBindingsForAccount(ctx, "missing"); err != nil || len(got) != 0 {
		t.Fatalf("list bindings missing = %+v, %v", got, err)
	}
	if got, err := m.ListGithubInstallBindingsForAccount(ctx, ""); err != nil || len(got) != 0 {
		t.Fatalf("list bindings empty = %+v, %v", got, err)
	}
	// DeleteGithubInstallBinding — hit + idempotent-miss + missing app.
	if err := m.DeleteGithubInstallBinding(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteGithubInstallBinding(ctx, app.ID); err != nil {
		t.Fatal("delete binding no-prior should be nil")
	}
	if err := m.DeleteGithubInstallBinding(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete binding empty = %v", err)
	}
	if err := m.DeleteGithubInstallBinding(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete binding missing app = %v", err)
	}

	// UpsertGitHubInstall — empty AccountID/AuditGithubLogin rejected.
	if err := m.UpsertGitHubInstall(ctx, GitHubInstall{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("upsert install empty = %v", err)
	}
	inst := GitHubInstall{AccountID: account.ID, InstallationID: 9, DefaultBranch: "main", SealedToken: []byte("sealed"), AuditGithubLogin: "alice@example.com"}
	if err := m.UpsertGitHubInstall(ctx, inst); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertGitHubInstall(ctx, GitHubInstall{AccountID: account.ID}); err == nil {
		t.Fatal("upsert install no audit login should fail")
	}
	// GitHubInstallForAccount — hit + miss + empty.
	if got, err := m.GitHubInstallForAccount(ctx, account.ID); err != nil || got.InstallationID != 9 {
		t.Fatalf("install for account = %+v, %v", got, err)
	}
	if _, err := m.GitHubInstallForAccount(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("install missing = %v", err)
	}
	if _, err := m.GitHubInstallForAccount(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("install empty = %v", err)
	}

	// RecordGitHubInstallationSync — invalid IDs are harmless, counters are
	// clamped, and an unknown installation is a no-op.
	if err := m.RecordGitHubInstallationSync(ctx, 0, time.Time{}, "ignored", -1, -1); err != nil {
		t.Fatalf("record sync invalid id = %v", err)
	}
	when := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if err := m.RecordGitHubInstallationSync(ctx, 9, when, "remote timeout", -4, -2); err != nil {
		t.Fatalf("record sync = %v", err)
	}
	got, err := m.GitHubInstallForAccountInstallation(ctx, account.ID, 9)
	if err != nil {
		t.Fatalf("read synced install = %v", err)
	}
	if got.LastReconciledAt == nil || !got.LastReconciledAt.Equal(when) || got.LastReconcileError != "remote timeout" || got.LastReconcileRepositoryCount != 0 || got.LastReconcileDetachedCount != 0 {
		t.Fatalf("synced install = %+v", got)
	}
	if err := m.RecordGitHubInstallationSync(ctx, 404, when, "missing", 3, 2); err != nil {
		t.Fatalf("record sync missing = %v", err)
	}
}

func TestMemStoreCoverageBuildProvenance(t *testing.T) {
	m, ctx, account, app, deployment := memCoverageSlice4Fixture(t)
	build, err := m.CreateBuild(ctx, deployment.ID, DeploymentKindDockerfile, 10, "/tmp/prov.log")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CreateBuildProvenance(ctx, BuildProvenance{BuildID: build.ID, BuildkitVer: "v0.20"}); err != nil {
		t.Fatal(err)
	}
	// UpdateBuildProvenanceSBOM — hit + miss.
	if err := m.UpdateBuildProvenanceSBOM(ctx, build.ID, "sboms/1"); err != nil {
		t.Fatal(err)
	}
	p, err := m.BuildProvenanceByBuildID(ctx, build.ID)
	if err != nil || p.SBOMStorageKey != "sboms/1" {
		t.Fatalf("provenance after sbom = %+v, %v", p, err)
	}
	if err := m.UpdateBuildProvenanceSBOM(ctx, "missing", "sboms/2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sbom missing = %v", err)
	}
	// CreateBuildProvenance with empty BuildID rejected.
	if err := m.CreateBuildProvenance(ctx, BuildProvenance{}); err == nil {
		t.Fatal("empty provenance build id should fail")
	}
	if _, err := m.BuildProvenanceByBuildID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("provenance missing = %v", err)
	}
	// Unused fixture vars guard against future signature drift.
	_ = account
	_ = app
}
