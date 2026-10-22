package e2e_test

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// Qualify the native journey's provider and HTTP approval boundary without
// synthesizing any workload or serving receipt. VM recovery has a separate gate.
func TestE2E_GitOpsSourceRevisionReviewAndRollback(t *testing.T) {
	if os.Getenv("GREGALE_GITOPS_ACCEPTANCE") != "1" {
		t.Skip("isolated GitOps PostgreSQL acceptance only")
	}
	if runtime.GOOS != "linux" {
		t.Skip("real apid capability admission requires Linux /proc")
	}
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) { runGitOpsSourceRevisionReviewAndRollback(t, provider) })
	}
}

func runGitOpsSourceRevisionReviewAndRollback(t *testing.T, providerName string) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	provider, providerEnv := newGitOpsProviderFixture(t, providerName)
	daemonEnv := append(providerEnv,
		"FAAS_ENVIRONMENT_GIT_DRIFT_REPORTING_ENABLED=false", "FAAS_ENVIRONMENT_GIT_SOURCE_POLLING_ENABLED=false",
		"FAAS_AUDIT_HMAC_KEY_FILE="+gitOpsAcceptanceHMACKeyFile(t, "audit-hmac.key"),
		"FAAS_RECOVERY_HMAC_KEY_FILE="+gitOpsAcceptanceHMACKeyFile(t, "recovery-hmac.key"))
	h := e2etest.Start(t, pool, e2etest.APID, daemonEnv...)
	key := h.SeedAccount(t.Context(), api.PlanPro, "gitops-source-review")
	store := state.NewPgStore(pool)
	account, err := store.AccountByEmail(t.Context(), "e2e+pro+gitops-source-review@test.example")
	if err != nil {
		t.Fatal(err)
	}
	connectGitOpsProvider(t, provider, store, account.ID)
	project, err := store.CreateProject(t.Context(), gitOpsProviderProject(providerName, "gitops-source-review", account.ID))
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api",
		WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	firstSHA, nextSHA := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, revision := range []struct{ sha, mode string }{{firstSHA, "original"}, {nextSHA, "changed"}} {
		provider.add(t, revision.sha, api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
			Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
				"api": {App: app.Slug, Variables: map[string]string{"MODE": revision.mode}},
			}})
	}
	client := api.NewClient(h.APIDURL, key)
	if _, err := client.CreateEnvironmentGitSource(t.Context(), project.Slug, "production", api.CreateEnvironmentGitSourceRequest{
		Provider: providerName, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "environments/production.yaml",
		Mode: "report", ApprovalPolicy: "manual"}); err != nil {
		t.Fatal(err)
	}
	approve := func(sha string) api.ApproveEnvironmentGitRevisionResponse {
		t.Helper()
		preview, err := client.PreviewEnvironmentGitRevision(t.Context(), project.Slug, "production",
			api.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.ApproveEnvironmentGitRevision(t.Context(), project.Slug, "production",
			api.ApproveEnvironmentGitRevisionRequest{CommitSHA: sha, DefinitionDigest: preview.DefinitionDigest, ExpectedGeneration: preview.Generation})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, next := approve(firstSHA), approve(nextSHA)
	if first.Revision.ID == next.Revision.ID || next.Source.Generation <= first.Source.Generation {
		t.Fatal("HTTP approval did not create a new immutable revision generation")
	}
	provider.setUnavailable(true)
	if _, err := client.PreviewEnvironmentGitRevision(t.Context(), project.Slug, "production",
		api.PreviewEnvironmentGitRevisionRequest{CommitSHA: firstSHA}); err == nil {
		t.Fatal("review succeeded through an unavailable source")
	}
	current, err := client.GetEnvironmentGitOps(t.Context(), project.Slug, "production")
	if err != nil || current.Source.ApprovedRevisionID != next.Revision.ID || current.Source.Generation != next.Source.Generation {
		t.Fatalf("source failure changed approval authority: %+v %v", current.Source, err)
	}
	provider.setUnavailable(false)
	rolledBack := approve(firstSHA)
	if rolledBack.Revision.ID != first.Revision.ID || rolledBack.Source.Generation <= next.Source.Generation || rolledBack.Source.AppliedRevisionID != "" {
		t.Fatalf("HTTP rollback did not retain immutable revision identity without claiming serving: %+v", rolledBack.Source)
	}
}
