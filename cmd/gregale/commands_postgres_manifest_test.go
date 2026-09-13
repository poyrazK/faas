package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeManifestPostgresClient struct {
	app         api.AppResponse
	appErr      error
	databases   api.ManagedPostgresDatabaseList
	listErr     error
	binding     api.ManagedPostgresBinding
	bindingErr  error
	requests    []api.CreateManagedPostgresBindingRequest
	databaseIDs []string
}

func (f *fakeManifestPostgresClient) GetApp(context.Context, string) (api.AppResponse, error) {
	return f.app, f.appErr
}

func (f *fakeManifestPostgresClient) ListManagedPostgresDatabases(context.Context) (api.ManagedPostgresDatabaseList, error) {
	return f.databases, f.listErr
}

func (f *fakeManifestPostgresClient) CreateManagedPostgresBinding(_ context.Context, databaseID string, request api.CreateManagedPostgresBindingRequest) (api.ManagedPostgresBinding, error) {
	f.databaseIDs = append(f.databaseIDs, databaseID)
	f.requests = append(f.requests, request)
	return f.binding, f.bindingErr
}

func TestDeployManifestPostgresBindingsCreatesReadyBinding(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `databases:
  - database: orders
    app: api
    scope: production
    env: DATABASE_URL
    access: read_only
`)
	fake := &fakeManifestPostgresClient{
		app:       api.AppResponse{ID: "app-id", Slug: "api"},
		databases: api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-id", Name: "orders", State: "ready"}}},
		binding:   api.ManagedPostgresBinding{ID: "binding-id", State: "ready", EnvironmentKey: "DATABASE_URL"},
	}
	previousJSON := jsonOutput
	jsonOutput = false
	t.Cleanup(func() { jsonOutput = previousJSON })
	if err := deployManifestPostgresBindings(context.Background(), fake, "api", dir); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(fake.requests) != 1 || fake.databaseIDs[0] != "db-id" {
		t.Fatalf("database IDs = %v, requests = %+v", fake.databaseIDs, fake.requests)
	}
	request := fake.requests[0]
	if request.AppID != "app-id" || request.Scope != "production" || request.EnvironmentKey != "DATABASE_URL" || request.Access != "read_only" {
		t.Fatalf("binding request = %+v", request)
	}
}

func TestDeployManifestPostgresBindingsUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "databases:\n  - database: orders\n")
	fake := &fakeManifestPostgresClient{
		app:       api.AppResponse{ID: "app-id", Slug: "api"},
		databases: api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-id", Name: "orders", State: "ready"}}},
		binding:   api.ManagedPostgresBinding{ID: "binding-id", State: "ready", EnvironmentKey: "DATABASE_URL"},
	}
	if err := deployManifestPostgresBindings(context.Background(), fake, "api", dir); err != nil {
		t.Fatalf("err = %v", err)
	}
	request := fake.requests[0]
	if request.Scope != api.DefaultEnvScope || request.EnvironmentKey != "DATABASE_URL" || request.Access != "read_write" {
		t.Fatalf("default binding request = %+v", request)
	}
}

func TestDeployManifestPostgresBindingsStopsOnNotReady(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "databases:\n  - database: orders\n")
	fake := &fakeManifestPostgresClient{
		app:       api.AppResponse{ID: "app-id", Slug: "api"},
		databases: api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-id", Name: "orders", State: "ready"}}},
		binding:   api.ManagedPostgresBinding{ID: "binding-id", State: "provisioning"},
	}
	err := deployManifestPostgresBindings(context.Background(), fake, "api", dir)
	if err == nil || !strings.Contains(err.Error(), "retry after") {
		t.Fatalf("err = %v, want actionable not-ready message", err)
	}
}

func TestResolveManagedPostgresDatabaseRejectsMissingReference(t *testing.T) {
	fake := &fakeManifestPostgresClient{databases: api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-id", Name: "orders"}}}}
	if _, err := resolveManagedPostgresDatabase(context.Background(), fake, "missing"); err == nil {
		t.Fatal("err = nil, want missing database reference")
	}
}

func TestResolveManagedPostgresDatabasePropagatesListError(t *testing.T) {
	fake := &fakeManifestPostgresClient{listErr: errors.New("temporary")}
	if _, err := resolveManagedPostgresDatabase(context.Background(), fake, "orders"); !errors.Is(err, fake.listErr) {
		t.Fatalf("err = %v, want list error", err)
	}
}

func TestManifestPostgresDeploymentScopeDefaultsAndPropagates(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "databases:\n  - database: orders\n    scope: production\n")
	got, err := manifestPostgresDeploymentScope("api", dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "production" {
		t.Fatalf("scope = %q, want production", got)
	}

	writeManifest(t, dir, "databases:\n  - database: orders\n")
	got, err = manifestPostgresDeploymentScope("api", dir)
	if err != nil {
		t.Fatalf("default err = %v", err)
	}
	if got != "" {
		t.Fatalf("default scope = %q, want omitted wire value", got)
	}
}

func TestManifestPostgresDeploymentScopeRejectsMixedScopes(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `databases:
  - database: orders
    scope: production
  - database: analytics
    scope: staging
`)
	if _, err := manifestPostgresDeploymentScope("api", dir); err == nil || !strings.Contains(err.Error(), "multiple scopes") {
		t.Fatalf("err = %v, want mixed-scope validation", err)
	}
}
