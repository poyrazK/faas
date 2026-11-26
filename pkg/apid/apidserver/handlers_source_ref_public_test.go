// adr: 20261126164847
package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const publicTestSHA = "fedcba9876543210fedcba9876543210fedcba98"

// publicSourceRefFake adds githubd's public-repository RPC to the
// installation-path recorder.
type publicSourceRefFake struct {
	*sourceRefFake

	publicCalls    int
	publicRepo     string
	publicRef      string
	publicMaxBytes int64
	publicBody     []byte
	publicStatsErr error
}

func (f *publicSourceRefFake) StreamPublicSourceRef(_ context.Context, repo, ref string, maxBytes int64) (*StreamSourceRefResult, error) {
	f.mux.Lock()
	defer f.mux.Unlock()
	f.publicCalls++
	f.publicRepo, f.publicRef, f.publicMaxBytes = repo, ref, maxBytes
	return &StreamSourceRefResult{
		Body: nopReadCloser{bytes.NewReader(f.publicBody)},
		Stats: &StreamSourceRefStats{
			BytesStreamed:     int64(len(f.publicBody)),
			ResolvedCommitSHA: publicTestSHA,
			Err:               f.publicStatsErr,
		},
	}, nil
}

// newPublicSourceTestEnv seeds an account with app "x" and, when
// installID > 0, an installation that sees no repository.
func newPublicSourceTestEnv(t *testing.T, installID int64) (sourceRefTestEnv, *publicSourceRefFake) {
	t.Helper()
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := context.Background()
	store := state.NewMemStore()
	gh := &publicSourceRefFake{
		sourceRefFake: &sourceRefFake{reposByInstall: map[int64][]Repo{}, repoErrByInstall: map[int64]error{}},
		publicBody:    buildSourceRefTarGz(t),
	}
	acct, err := store.CreateAccount(ctx, "public-src@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "x", Status: state.AppActive})
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if installID > 0 {
		if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: acct.ID, InstallationID: installID, AuditGithubLogin: "alice"}); err != nil {
			t.Fatalf("seed install: %v", err)
		}
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev",
		noopNotifier{}, "", noopMailer{}, gh, nil, nil, 15*60_000_000_000, "")
	srv.EnablePlatformFlagForAll(platformflags.PublicSourceDeploys)
	return sourceRefTestEnv{h: srv.handler(), srv: srv, store: store, gh: gh.sourceRefFake, key: pt, acctID: acct.ID, appID: app.ID}, gh
}

func deploySourceRefAuditData(t *testing.T, e sourceRefTestEnv) map[string]any {
	t.Helper()
	rows, err := e.store.ListEvents(context.Background(), e.acctID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, row := range rows {
		if row.Kind == "deploy.source_ref" {
			var data map[string]any
			if err := json.Unmarshal(row.Data, &data); err != nil {
				t.Fatalf("audit data: %v", err)
			}
			return data
		}
	}
	t.Fatalf("no deploy.source_ref audit row")
	return nil
}

// A repository no installation of the account sees deploys as a public
// repository: pinned to the resolved commit, with no branch intent and no
// installation on the row.
func TestSourceRefPublic_DeploysWithoutInstallation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installID int64
	}{
		{name: "no installation", installID: 0},
		{name: "installation does not see the repository", installID: 101},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, gh := newPublicSourceTestEnv(t, tc.installID)
			rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{Repo: "gregale-dev/starter", Ref: "main"})
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
			}
			if gh.publicCalls != 1 || gh.publicRepo != "gregale-dev/starter" || gh.publicRef != "main" || gh.publicMaxBytes != int64(250)*1024*1024 {
				t.Fatalf("public stream = calls:%d repo:%q ref:%q max:%d", gh.publicCalls, gh.publicRepo, gh.publicRef, gh.publicMaxBytes)
			}
			if gh.streamCalls != 0 || gh.branchCalls != 0 {
				t.Fatalf("installation stream calls = %d, branch lookups = %d; want 0 and 0", gh.streamCalls, gh.branchCalls)
			}
			deps, err := e.store.ListDeploymentsForApp(context.Background(), e.appID, 0, 0)
			if err != nil || len(deps) != 1 {
				t.Fatalf("deployments = %d, %v; want 1", len(deps), err)
			}
			dep := deps[0]
			if dep.CommitSHA != publicTestSHA || dep.SourceURL != "github://gregale-dev/starter@"+publicTestSHA || dep.GitHubSourceRef != "" || dep.GitHubInstallationID != 0 {
				t.Fatalf("deployment = sha:%q url:%q branch:%q install:%d; want a commit-pinned public deploy", dep.CommitSHA, dep.SourceURL, dep.GitHubSourceRef, dep.GitHubInstallationID)
			}
			data := deploySourceRefAuditData(t, e)
			if data["access"] != "public" || data["install_id"] != float64(0) || data["source_sha"] != publicTestSHA {
				t.Fatalf("audit = %v, want access public, install_id 0, source_sha %s", data, publicTestSHA)
			}
		})
	}
}

// A repository that is not public, or a githubd without the public RPC,
// gets exactly the installation path's answer.
func TestSourceRefPublic_NotPublicKeepsInstallationAnswer(t *testing.T) {
	notPublic := api.NewProblem(http.StatusForbidden, api.CodeGitHubRepoNotAccessible, "Repository is not accessible", "private")
	oldGithubd := status.Error(codes.Unimplemented, "unknown method StreamPublicSourceRef")
	for _, tc := range []struct {
		name       string
		installID  int64
		statsErr   error
		wantStatus int
		wantCode   string
	}{
		{name: "private, no installation", statsErr: notPublic, wantStatus: http.StatusNotFound, wantCode: api.CodeGitHubInstallNotFound},
		{name: "private, installation does not see it", installID: 101, statsErr: notPublic, wantStatus: http.StatusForbidden, wantCode: api.CodeGitHubRepoNotAccessible},
		{name: "githubd without the RPC", statsErr: oldGithubd, wantStatus: http.StatusNotFound, wantCode: api.CodeGitHubInstallNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, gh := newPublicSourceTestEnv(t, tc.installID)
			gh.publicBody = nil
			gh.publicStatsErr = tc.statsErr
			rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{Repo: "acme/private", Ref: "main"})
			if rec.Code != tc.wantStatus || bodyCode(t, rec) != tc.wantCode {
				t.Fatalf("status/code = %d/%q, want %d/%q; body=%s", rec.Code, bodyCode(t, rec), tc.wantStatus, tc.wantCode, rec.Body)
			}
		})
	}
}

// An app bound to the repository whose installation is gone keeps the
// installation answer: a public deploy would leave push-to-deploy silently
// broken behind a stale binding.
func TestSourceRefPublic_BoundAppKeepsInstallationAnswer(t *testing.T) {
	e, gh := newPublicSourceTestEnv(t, 0)
	if err := e.store.UpsertGithubInstallBinding(context.Background(), state.GitHubBinding{
		AppID: e.appID, AccountID: e.acctID, BindingID: uuid.NewString(), InstallID: 404,
		RepoFullName: "Gregale-Dev/Starter", ProductionBranch: "main",
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{Repo: "gregale-dev/starter", Ref: "main"})
	if rec.Code == http.StatusAccepted || gh.publicCalls != 0 {
		t.Fatalf("status = %d, public calls = %d; want the installation refusal and no public read; body=%s", rec.Code, gh.publicCalls, rec.Body)
	}
	if code := bodyCode(t, rec); code != api.CodeGitHubInstallNotFound && code != api.CodeGitHubRepoNotAccessible {
		t.Fatalf("code = %q, want the installation path's refusal", code)
	}
	// A binding for another repository does not block a public deploy of
	// this one.
	other := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{Repo: "gregale-dev/other", Ref: "main"})
	if other.Code != http.StatusAccepted || gh.publicCalls != 1 {
		t.Fatalf("other repo status = %d, public calls = %d; want 202 and one public read; body=%s", other.Code, gh.publicCalls, other.Body)
	}
}

// While public_source_deploys is off for the account, apid answers exactly
// as it did before public reads existed and never asks githubd.
func TestSourceRefPublic_FlagOffKeepsInstallationAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  platformflags.Definition
	}{
		{name: "flag never written", def: platformflags.Definition{}},
		{name: "released to another account", def: platformflags.FromAccountList(true, false, []string{uuid.NewString()})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, gh := newPublicSourceTestEnv(t, 0)
			e.srv.SeedPlatformFlag(platformflags.PublicSourceDeploys, tc.def)
			rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{Repo: "gregale-dev/starter", Ref: "main"})
			if rec.Code != http.StatusNotFound || bodyCode(t, rec) != api.CodeGitHubInstallNotFound || gh.publicCalls != 0 {
				t.Fatalf("status/code = %d/%q, public calls = %d; want 404 install-not-found and none; body=%s", rec.Code, bodyCode(t, rec), gh.publicCalls, rec.Body)
			}
			scan := e.post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{Repo: "gregale-dev/starter", Ref: "main", ProjectSlug: "starter"})
			if scan.Code == http.StatusOK || gh.publicCalls != 0 {
				t.Fatalf("scan status = %d, public calls = %d; want a refusal and none", scan.Code, gh.publicCalls)
			}
		})
	}
}

// Failures that are not "no installation sees it" never fall back, and
// branch-pinned deploys keep requiring an installation.
func TestSourceRefPublic_NoFallback(t *testing.T) {
	t.Run("installation catalog unavailable", func(t *testing.T) {
		e, gh := newPublicSourceTestEnv(t, 101)
		gh.repoErrByInstall[101] = api.ErrSourceRefUnavailable("GitHub is down")
		rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{Repo: "gregale-dev/starter", Ref: "main"})
		if rec.Code != http.StatusServiceUnavailable || gh.publicCalls != 0 {
			t.Fatalf("status = %d, public calls = %d; want 503 and none; body=%s", rec.Code, gh.publicCalls, rec.Body)
		}
	})
	t.Run("source branch", func(t *testing.T) {
		e, gh := newPublicSourceTestEnv(t, 0)
		rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{Repo: "gregale-dev/starter", Ref: publicTestSHA, SourceBranch: "main"})
		if rec.Code != http.StatusNotFound || bodyCode(t, rec) != api.CodeGitHubInstallNotFound || gh.publicCalls != 0 {
			t.Fatalf("status/code = %d/%q, public calls = %d; want 404 install-not-found and none", rec.Code, bodyCode(t, rec), gh.publicCalls)
		}
	})
}

func TestSourceRefPublic_RateLimitedPerAccount(t *testing.T) {
	e, gh := newPublicSourceTestEnv(t, 0)
	acct := state.Account{ID: e.acctID}
	for i := range api.PublicSourceFetchesPerHour {
		if p := e.srv.takePublicSourceFetch(acct); p != nil {
			t.Fatalf("fetch %d refused: %v", i+1, p)
		}
	}
	rec := e.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{Repo: "gregale-dev/starter", Ref: "main"})
	if rec.Code != http.StatusTooManyRequests || bodyCode(t, rec) != api.CodePublicSourceFetchRateLimited || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status/code = %d/%q retry=%q, want 429/%s with Retry-After; body=%s", rec.Code, bodyCode(t, rec), rec.Header().Get("Retry-After"), api.CodePublicSourceFetchRateLimited, rec.Body)
	}
	if gh.publicCalls != 0 {
		t.Fatalf("public calls = %d past the limit", gh.publicCalls)
	}
	// Another account has its own budget.
	if p := e.srv.takePublicSourceFetch(state.Account{ID: "other-account"}); p != nil {
		t.Fatalf("other account refused: %v", p)
	}
}

// A scan of a public repository reads it the same way and carries no
// repository binding into the plan.
func TestProjectSourceRefScanPublicRepository(t *testing.T) {
	e, gh := newPublicSourceTestEnv(t, 0)
	rec := e.post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{Repo: "gregale-dev/starter", Ref: "main", ProjectSlug: "starter"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if plan.ProjectSlug != "starter" || plan.RepoFullName != "" {
		t.Fatalf("plan = slug:%q repo:%q, want starter with no repository binding", plan.ProjectSlug, plan.RepoFullName)
	}
	if gh.publicCalls != 1 || gh.streamCalls != 0 {
		t.Fatalf("public calls = %d, installation calls = %d; want 1 and 0", gh.publicCalls, gh.streamCalls)
	}
}

// A scan that names an installation never swaps it for a public read.
func TestProjectSourceRefScanChosenInstallationNoFallback(t *testing.T) {
	e, gh := newPublicSourceTestEnv(t, 0)
	rec := e.post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{Repo: "gregale-dev/starter", Ref: "main", ProjectSlug: "starter", InstallID: 555})
	if rec.Code == http.StatusOK || gh.publicCalls != 0 {
		t.Fatalf("status = %d, public calls = %d; want a refusal and no public read", rec.Code, gh.publicCalls)
	}
}

func TestPublicReadErrorKeepsOtherFailures(t *testing.T) {
	access := sourceRefAccess{public: true, installProblem: api.ErrGitHubInstallNotFound()}
	unavailable := api.ErrSourceRefUnavailable("rate limited")
	if got := access.publicReadError(unavailable); !errors.Is(got, unavailable) {
		t.Fatalf("publicReadError(unavailable) = %v, want it unchanged", got)
	}
	if got := access.publicReadError(nil); got != nil {
		t.Fatalf("publicReadError(nil) = %v", got)
	}
}
