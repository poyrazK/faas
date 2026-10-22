package e2e_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// This exercises report-only reconciliation through a real apid process and
// PostgreSQL. Reviewed approval and adoption are seeded at the state boundary;
// HTTP review and native qualification have separate acceptance tests.
func TestE2E_EnvironmentGitOpsReportSurvivesAPIDRestart(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "gitops-report-e2e@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "shop",
		RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID,
		ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID,
		Slug: "shop-api", WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []struct{ scope, key, value string }{
		{"production", "MODE", "console"},
		{"production", "MANUAL", "keep"},
		{"staging", "MODE", "staging"},
	} {
		if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, variable.scope, variable.key, variable.value); err != nil {
			t.Fatal(err)
		}
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production",
		state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/shop",
			Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "report", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
		Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
			"api": {App: app.Slug, Variables: map[string]string{"MODE": "approved"}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{
		AccountID: account.ID, SourceID: source.ID, ExpectedGeneration: source.Generation,
		CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "e2e-owner"})
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), account.ID, source.ID)
	if err != nil || !adoption.CanApply() {
		t.Fatalf("adoption preview: %+v %v", adoption, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), account.ID, source.ID, adoption.Hash); err != nil {
		t.Fatal(err)
	}

	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account.ID, hash, "gitops report e2e", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_ENVIRONMENT_GIT_DRIFT_REPORTING_ENABLED=true",
		"FAAS_ENVIRONMENT_GIT_SOURCE_POLLING_ENABLED=false",
	})
	client := api.NewClient(h.APIDURL, key)
	first := waitEnvironmentGitOpsReport(t, store, source, "")
	if first.Status != "drifted" || first.RevisionID != source.ApprovedRevisionID {
		t.Fatalf("first report: %+v", first)
	}
	assertGitOpsReportVariables(t, store, account.ID, app.ID, "console")
	status, err := client.GetEnvironmentGitOps(t.Context(), project.Slug, "production")
	if err != nil || status.Source.ApprovedRevisionID != source.ApprovedRevisionID ||
		len(status.Runs) == 0 || status.Runs[0].ID != first.ID || status.Source.AppliedRevisionID != "" {
		t.Fatalf("API status before restart: %+v %v", status, err)
	}

	if err := h.RestartAPID(); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "second-console-edit"); err != nil {
		t.Fatal(err)
	}
	second := waitEnvironmentGitOpsReport(t, store, source, first.ID)
	if second.Status != "drifted" || second.RevisionID != first.RevisionID {
		t.Fatalf("report after API restart: %+v", second)
	}
	assertGitOpsReportVariables(t, store, account.ID, app.ID, "second-console-edit")
	status, err = client.GetEnvironmentGitOps(t.Context(), project.Slug, "production")
	if err != nil || len(status.Runs) < 2 || status.Runs[0].ID != second.ID ||
		status.Source.AppliedRevisionID != "" {
		t.Fatalf("API status after restart: %+v %v", status, err)
	}
}

func waitEnvironmentGitOpsReport(t *testing.T, store *state.PgStore, source state.EnvironmentGitSource, previousID string) state.EnvironmentGitOpsRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		runs, err := store.ListEnvironmentGitOpsRuns(ctx, source.AccountID, source.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) == 1 && runs[0].CompletedAt != nil && runs[0].ID != previousID {
			return runs[0]
		}
		select {
		case <-ctx.Done():
			t.Fatalf("apid did not publish a new GitOps report: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertGitOpsReportVariables(t *testing.T, store *state.PgStore, accountID, appID, mode string) {
	t.Helper()
	for _, check := range []struct{ scope, key, value string }{
		{"production", "MODE", mode},
		{"production", "MANUAL", "keep"},
		{"staging", "MODE", "staging"},
	} {
		variables, err := store.ListAppEnvInScope(t.Context(), accountID, appID, check.scope)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, variable := range variables {
			if variable.Key == check.key && variable.Value == check.value {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s/%s changed during report: %+v", check.scope, check.key, variables)
		}
	}
}
