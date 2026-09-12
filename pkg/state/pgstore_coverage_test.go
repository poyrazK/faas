package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This file mirrors the MemStore coverage slices (4-10) against the
// real PgStore + Postgres. Every test uses pgStore(t), which stands up
// a fresh migrated schema and skips when DATABASE_URL is unset — the
// same harness the CI coverage job runs. These round-trips close the
// 0%-coverage CRUD surface in pgstore.go (accounts, keys, projects,
// github bindings, builds, domains, crons). pgCoverageFixture comes
// from pgstore_coverage_parity_test.go (same package).

func TestPg_CoverageAccountsAndKeys(t *testing.T) {
	s, ctx, account, _, _ := pgCoverageFixture(t)
	// AccountByEmail hit + miss.
	if got, err := s.AccountByEmail(ctx, account.Email); err != nil || got.ID != account.ID {
		t.Fatalf("account by email = %+v, %v", got, err)
	}
	if _, err := s.AccountByEmail(ctx, "missing@example.com"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("account by email missing = %v", err)
	}
	// UpdateAccountPlan + UpdateAccountStatus.
	if err := s.UpdateAccountPlan(ctx, account.ID, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountStatus(ctx, account.ID, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	acct, err := s.AccountByID(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Plan != api.PlanScale || acct.Status != state.AccountPastDue {
		t.Fatalf("updated account = %+v", acct)
	}
	org, err := s.OrgByPersonalAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if org.Plan != api.PlanScale || org.Status != state.OrgStatus(state.AccountPastDue) {
		t.Fatalf("personal org entitlement drifted: %+v", org)
	}
	// CreateAPIKey + APIKeyByHash + AuthenticateKey + AccountByKeyHash.
	// Scopes must satisfy the DB vocab CHECK
	// (api_keys_scopes_vocab_chk): subset of the six allowed values,
	// cardinality > 0.
	hash := []byte("pg-cov-key-hash")
	key, err := s.CreateAPIKey(ctx, account.ID, hash, "pg-cov", []string{"apps:read", "deploy:write"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.APIKeyByHash(ctx, hash); err != nil || got.ID != key.ID || len(got.Scopes) != 2 {
		t.Fatalf("key by hash = %+v, %v", got, err)
	}
	if gotAcct, gotKey, err := s.AuthenticateKey(ctx, hash); err != nil || gotAcct.ID != account.ID || gotKey.ID != key.ID {
		t.Fatalf("authenticate = %+v/%+v, %v", gotAcct, gotKey, err)
	}
	if gotAcct, err := s.AccountByKeyHash(ctx, hash); err != nil || gotAcct.ID != account.ID {
		t.Fatalf("account by key hash = %+v, %v", gotAcct, err)
	}
	// DeleteAPIKey + DeleteAPIKeyReturning (hit + miss).
	if err := s.DeleteAPIKey(ctx, account.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAPIKey(ctx, account.ID, key.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delete key repeat = %v", err)
	}
	key2, err := s.CreateAPIKey(ctx, account.ID, []byte("pg-cov-key-hash-2"), "pg-cov-2", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	del, err := s.DeleteAPIKeyReturning(ctx, account.ID, key2.ID)
	if err != nil || del.ID != key2.ID {
		t.Fatalf("delete key returning = %+v, %v", del, err)
	}
	if _, err := s.DeleteAPIKeyReturning(ctx, account.ID, key2.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delete key returning repeat = %v", err)
	}
	// TouchKeyLastUsed.
	key3, err := s.CreateAPIKey(ctx, account.ID, []byte("pg-cov-key-hash-3"), "pg-cov-3", []string{"usage:read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TouchKeyLastUsed(ctx, key3.ID); err != nil {
		t.Fatal(err)
	}
	// ListAPIKeys.
	if got, err := s.ListAPIKeys(ctx, account.ID); err != nil || len(got) != 1 || got[0].ID != key3.ID {
		t.Fatalf("list keys = %+v, %v", got, err)
	}
}

func TestPg_CoverageRecoveryCodeMatch(t *testing.T) {
	s, ctx, account, _, _ := pgCoverageFixture(t)
	// SetMFASecret + MatchRecoveryCode hit/miss + ConsumeRecoveryCode.
	if err := s.SetMFASecret(ctx, account.ID, []byte("sealed"), [][]byte{[]byte("h1"), []byte("h2")}); err != nil {
		t.Fatal(err)
	}
	if matched, last, err := s.MatchRecoveryCode(ctx, account.ID, []byte("h1")); err != nil || !matched || last {
		t.Fatalf("match h1 = %v/%v, %v", matched, last, err)
	}
	if matched, last, err := s.MatchRecoveryCode(ctx, account.ID, []byte("nope")); err != nil || matched || last {
		t.Fatalf("match miss = %v/%v, %v", matched, last, err)
	}
	if _, _, err := s.MatchRecoveryCode(ctx, uuid.NewString(), []byte("h1")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("match missing = %v", err)
	}
	if matched, last, remaining, err := s.ConsumeRecoveryCode(ctx, account.ID, []byte("h1")); err != nil || !matched || last || remaining != 1 {
		t.Fatalf("consume h1 = %v/%v/%d, %v", matched, last, remaining, err)
	}
	if matched, last, remaining, err := s.ConsumeRecoveryCode(ctx, account.ID, []byte("h2")); err != nil || !matched || !last || remaining != 0 {
		t.Fatalf("consume h2 = %v/%v/%d, %v", matched, last, remaining, err)
	}
}

func TestPg_CoverageProjectsAndGithubBindings(t *testing.T) {
	s, ctx, account, app, _ := pgCoverageFixture(t)
	missingID := uuid.NewString()
	// CreateProject + ProjectBySlug + DeleteProject.
	proj, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "pg-proj"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ProjectBySlug(ctx, account.ID, "pg-proj"); err != nil || got.ID != proj.ID {
		t.Fatalf("project by slug = %+v, %v", got, err)
	}
	if err := s.DeleteProject(ctx, proj.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectBySlug(ctx, account.ID, "pg-proj"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("project after delete = %v", err)
	}
	// RecordGitHubBinding + GitHubBindingForApp + InstallationIDForRepo.
	if err := s.RecordGitHubBinding(ctx, app.ID, 42, "acme/pg-app", "main"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GitHubBindingForApp(ctx, app.ID); err != nil || got.InstallID != 42 || got.RepoFullName != "acme/pg-app" {
		t.Fatalf("binding for app = %+v, %v", got, err)
	}
	if got, err := s.InstallationIDForRepo(ctx, "acme/pg-app"); err != nil || got != 42 {
		t.Fatalf("install for repo = %d, %v", got, err)
	}
	if _, err := s.InstallationIDForRepo(ctx, "no/repo"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("install missing = %v", err)
	}
	if _, err := s.GitHubBindingForApp(ctx, missingID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("binding missing = %v", err)
	}
	// UpsertGithubInstallBinding + GetGithubInstallBindingForApp.
	b := state.GitHubBinding{AppID: app.ID, AccountID: account.ID, BindingID: "bind-pg", InstallID: 43, RepoFullName: "acme/pg-app", ProductionBranch: "main"}
	if err := s.UpsertGithubInstallBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetGithubInstallBindingForApp(ctx, app.ID, account.ID); err != nil || got.BindingID != "bind-pg" {
		t.Fatalf("get binding = %+v, %v", got, err)
	}
	if _, err := s.GetGithubInstallBindingForApp(ctx, app.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("get binding wrong account = %v", err)
	}
	// DeleteGithubInstallBinding (idempotent).
	if err := s.DeleteGithubInstallBinding(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGithubInstallBinding(ctx, app.ID); err != nil {
		t.Fatal("delete binding no-prior should be nil")
	}
}

func TestPg_CoverageBuildsAndProvenance(t *testing.T) {
	s, ctx, account, app, deployment := pgCoverageFixture(t)
	// CreateBuild + BuildByDeployment + ClaimNextQueuedBuild. Build kind
	// must satisfy builds_kind_check ('railpack','dockerfile','tarball',
	// 'github').
	build, err := s.CreateBuild(ctx, deployment.ID, state.DeploymentKindDockerfile, 10, "/tmp/pg.log")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.BuildByDeployment(ctx, deployment.ID); err != nil || got.ID != build.ID {
		t.Fatalf("build by deployment = %+v, %v", got, err)
	}
	claimed, err := s.ClaimNextQueuedBuild(ctx)
	if err != nil || claimed.ID != build.ID {
		t.Fatalf("claim next = %+v, %v", claimed, err)
	}
	if _, err := s.ClaimNextQueuedBuild(ctx); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("drained = %v", err)
	}
	// RequeueBuild + RecordRecentBuildClaim + ClaimNextQueuedBuildWithFairness.
	if err := s.RequeueBuild(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRecentBuildClaim(ctx, account.ID, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextQueuedBuildWithFairness(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	// CreateBuildProvenance + BuildProvenanceByBuildID + UpdateBuildProvenanceSBOM.
	if err := s.CreateBuildProvenance(ctx, state.BuildProvenance{BuildID: claimed.ID, BuildkitVer: "v0.20"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.BuildProvenanceByBuildID(ctx, claimed.ID); err != nil || got.BuildID != claimed.ID {
		t.Fatalf("provenance = %+v, %v", got, err)
	}
	if err := s.UpdateBuildProvenanceSBOM(ctx, claimed.ID, "sboms/pg-1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.BuildProvenanceByBuildID(ctx, claimed.ID)
	if err != nil || got.SBOMStorageKey != "sboms/pg-1" {
		t.Fatalf("provenance sbom = %+v, %v", got, err)
	}
	if _, err := s.BuildProvenanceByBuildID(ctx, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("provenance missing = %v", err)
	}
	_ = app
}

func TestPg_CoverageDomainsAndCrons(t *testing.T) {
	s, ctx, account, app, _ := pgCoverageFixture(t)
	// CreateCustomDomain + DomainByName + ListDomainsForApp + MarkDomainVerified + DeleteCustomDomain.
	dom, err := s.CreateCustomDomain(ctx, "pg.example.com", app.ID, "tok")
	if err != nil {
		t.Fatal(err)
	}
	_ = dom
	if got, err := s.DomainByName(ctx, "pg.example.com"); err != nil || got.Domain != "pg.example.com" {
		t.Fatalf("domain by name = %+v, %v", got, err)
	}
	if got, err := s.ListDomainsForApp(ctx, app.ID); err != nil || len(got) != 1 {
		t.Fatalf("domains for app = %+v, %v", got, err)
	}
	if err := s.MarkDomainVerified(ctx, "pg.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCustomDomain(ctx, "pg.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DomainByName(ctx, "pg.example.com"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("domain after delete = %v", err)
	}
	// CreateCron + ListCronsForApp + DeleteCron + MarkCronFired.
	cron, err := s.CreateCron(ctx, app.ID, "* * * * *", "/health", true)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListCronsForApp(ctx, app.ID); err != nil || len(got) != 1 || got[0].ID != cron.ID {
		t.Fatalf("crons for app = %+v, %v", got, err)
	}
	if err := s.MarkCronFired(ctx, cron.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCron(ctx, cron.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCron(ctx, cron.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delete cron repeat = %v", err)
	}
	_ = account
}

func TestPg_CoverageWildcardDomainsAndTenantSurfaceHostnames(t *testing.T) {
	s, ctx, account, app, _ := pgCoverageFixture(t)

	if _, err := s.CreateCustomDomain(ctx, "*.wildcard.example.com", app.ID, "wildcard"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCustomDomain(ctx, "*.sub.wildcard.example.com", app.ID, "wildcard-sub"); err != nil {
		t.Fatal(err)
	}
	got, err := s.WildcardDomainForHost(ctx, "api.sub.wildcard.example.com")
	if err != nil || got.Domain != "*.sub.wildcard.example.com" {
		t.Fatalf("wildcard lookup = %+v, %v", got, err)
	}
	if _, err := s.WildcardDomainForHost(ctx, "wildcard.example.com"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("wildcard apex lookup = %v, want ErrNotFound", err)
	}

	limits := api.Limits{
		TenantSurfacesAllowed:     true,
		TenantSurfacesPerAccount:  2,
		TenantHostnamesPerSurface: 2,
	}
	surface, err := s.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: account.ID, AppID: app.ID, Name: "global-hostnames",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: surface.ID, Hostname: "tenant.global.example.com", ChallengeToken: "tenant-token",
	}, limits); err != nil {
		t.Fatal(err)
	}
	hostnames, err := s.ListTenantSurfaceHostnames(ctx)
	if err != nil || len(hostnames) != 1 || hostnames[0] != "tenant.global.example.com" {
		t.Fatalf("global tenant hostnames = %#v, %v", hostnames, err)
	}
}
