// adr: 211
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentStateAndUnifiedDiff(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	createProjectEnvironmentConfigFixture(t, store, acct.ID, project.ID, "production", `{"region":"us"}`)
	createProjectEnvironmentConfigFixture(t, store, acct.ID, project.ID, "staging", `{"region":"eu","checkout":true}`)

	if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, "production", "MODE", "production"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, "staging", "MODE", "staging"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, "staging", "FEATURE_CHECKOUT", "true"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, acct.ID, app.ID, "production", "STRIPE_KEY", "age1-production", "1111111111111111", []byte("sealed-production")); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, acct.ID, app.ID, "staging", "STRIPE_KEY", "age1-staging", "2222222222222222", []byte("sealed-staging")); err != nil {
		t.Fatal(err)
	}
	if err := store.PutManagedPostgresSecret(ctx, state.AppSecret{
		AccountID: acct.ID, AppID: app.ID, Scope: "staging", Key: "DATABASE_URL",
		Ciphertext: []byte("sealed-database"), ValueHash: "3333333333333333",
		ManagedPostgresBindingID: "binding-staging", ManagedCredentialRef: "credential-staging",
		ManagedCredentialGeneration: 7,
	}); err != nil {
		t.Fatal(err)
	}

	production, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "production", Status: state.DeployLive,
		ImageDigest: "sha256:production", CommitSHA: "production-sha",
	})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "staging", Status: state.DeployLive,
		ImageDigest: "sha256:staging", CommitSHA: "staging-sha",
	})
	if err != nil {
		t.Fatal(err)
	}

	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/staging/state", "shop", nil)
	req.SetPathValue("environment", "staging")
	srv.getProjectEnvironmentState(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("state status=%d body=%s", rec.Code, rec.Body.String())
	}
	var snapshot api.ProjectEnvironmentStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Environment != "staging" || len(snapshot.Workloads) != 1 || snapshot.Workloads[0].Release.DeploymentID != staging.ID {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	workload := snapshot.Workloads[0]
	if len(workload.Variables) != 2 || len(workload.Secrets) != 2 || len(workload.Bindings) != 1 || workload.Bindings[0].CredentialGeneration != 7 {
		t.Fatalf("workload state=%+v", workload)
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"sealed-staging", "sealed-database", "age1-staging"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("state response leaked %q: %s", forbidden, body)
		}
	}

	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/environments/staging/diff?from=production", "shop", nil)
	req.SetPathValue("environment", "staging")
	srv.diffProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff status=%d body=%s", rec.Code, rec.Body.String())
	}
	var diff api.ProjectEnvironmentDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatal(err)
	}
	if diff.FromEnvironment != "production" || diff.ToEnvironment != "staging" || len(diff.Configuration.Changes) != 2 {
		t.Fatalf("diff header/config=%+v", diff)
	}
	if len(diff.Workloads) != 1 || diff.Workloads[0].Release.Kind != "changed" || diff.Workloads[0].Release.Before.DeploymentID != production.ID {
		t.Fatalf("release diff=%+v", diff.Workloads)
	}
	if len(diff.Workloads[0].Variables) != 2 || len(diff.Workloads[0].Secrets) != 2 || len(diff.Workloads[0].Bindings) != 1 {
		t.Fatalf("workload diff=%+v", diff.Workloads[0])
	}
	if diff.Workloads[0].Secrets[0].Before.ValueHash != "" && diff.Workloads[0].Secrets[0].Key == "DATABASE_URL" {
		t.Fatalf("missing secret unexpectedly has a fingerprint: %+v", diff.Workloads[0].Secrets[0])
	}
}

func createProjectEnvironmentConfigFixture(t *testing.T, store *state.MemStore, accountID, projectID, environment, raw string) {
	t.Helper()
	values, hash, err := api.NormalizeProjectEnvironmentConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(context.Background(), state.ProjectEnvironmentConfig{
		AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment,
		ConfigHash: hash, Values: values,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectEnvironmentDiffRejectsSameEnvironment(t *testing.T) {
	srv, _, acct, _, _ := newProjectLifecycleFixture(t)
	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/diff?from=production", "shop", nil)
	req.SetPathValue("environment", "production")
	srv.diffProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
