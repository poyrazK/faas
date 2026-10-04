// adr: 567
package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentCloneIncludesLegacyProductionValues(t *testing.T) {
	srv, store, acct, _, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, "default", "MODE", "legacy-production"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, acct.ID, app.ID, "default", "TOKEN", "age1-test", "1111111111111111", []byte("sealed-legacy")); err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop", []byte(`{"slug":"staging","from_environment":"production"}`))
	srv.createProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("legacy clone status=%d body=%s", rec.Code, rec.Body.String())
	}
	values, err := store.ListAppEnvInScope(ctx, acct.ID, app.ID, "staging")
	if err != nil || len(values) != 1 || values[0].Value != "legacy-production" {
		t.Fatalf("legacy runtime values omitted: %+v, %v", values, err)
	}
	secrets, err := store.ListAppSecretsInScope(ctx, acct.ID, app.ID, "staging")
	if err != nil || len(secrets) != 1 || string(secrets[0].Ciphertext) != "sealed-legacy" {
		t.Fatalf("legacy secrets omitted: %+v, %v", secrets, err)
	}
}

func TestProjectEnvironmentCloneFailsClosedForLegacyManagedBinding(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if err := store.PutManagedPostgresSecret(ctx, state.AppSecret{
		AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: "DATABASE_URL",
		Ciphertext: []byte("sealed-managed"), ManagedPostgresBindingID: "binding-legacy", ManagedPostgresAccess: "read_write",
		ManagedCredentialRef: "credential-legacy", ManagedCredentialGeneration: 3,
	}); err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop", []byte(`{"slug":"staging","from_environment":"production"}`))
	srv.createProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("legacy managed resource was omitted: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unavailable managed resource left target: %v", err)
	}
}

func TestProjectEnvironmentBindingPlanUsesCapturedValueScope(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if err := store.PutManagedPostgresSecret(ctx, state.AppSecret{
		AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: "DATABASE_URL",
		Ciphertext: []byte("sealed-managed"), ManagedPostgresBindingID: "binding-legacy", ManagedPostgresAccess: "read_write",
		ManagedCredentialRef: "credential-legacy", ManagedCredentialGeneration: 3,
	}); err != nil {
		t.Fatal(err)
	}
	apps, snapshot, err := srv.captureProjectEnvironmentValues(ctx, acct, project, "production")
	if err != nil || snapshot.ValueScopes[app.ID] != "default" {
		t.Fatalf("capture legacy values: %v, %v", snapshot.ValueScopes, err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Status: state.DeployPending, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.planProjectEnvironmentBindingClones(ctx, acct, apps, snapshot.ValueScopes, false); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("binding preparation skipped the captured legacy binding after cutover: %v", err)
	}
}

type cloneValuesChangingStore struct {
	*state.MemStore
	appID string
}

func (s *cloneValuesChangingStore) CloneProjectEnvironment(ctx context.Context, clone state.ProjectEnvironmentClone, limits api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error) {
	if err := s.UpsertAppEnvInScope(ctx, clone.AccountID, s.appID, "default", "MODE", "changed-during-preparation"); err != nil {
		return state.ProjectEnvironment{}, state.ProjectEnvironmentCloneResult{}, err
	}
	return s.MemStore.CloneProjectEnvironment(ctx, clone, limits)
}

func TestProjectEnvironmentCloneRejectsValuesChangedAfterCapture(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	srv.store = &cloneValuesChangingStore{MemStore: store, appID: app.ID}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop", []byte(`{"slug":"staging","from_environment":"production"}`))
	srv.createProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusConflict {
		t.Fatalf("changed source values were accepted: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := store.ProjectEnvironmentBySlug(context.Background(), acct.ID, project.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected clone left target: %v", err)
	}
}
