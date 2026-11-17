package main_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type driftManifestPostgresClient struct {
	fakeManifestPostgresClient
	live api.AppPostgresBindingList
}

func (f *driftManifestPostgresClient) ListAppPostgresBindings(context.Context, string) (api.AppPostgresBindingList, error) {
	return f.live, nil
}

func TestDeployManifestPostgresBindingsWarnsAboutUndeclaredBindings(t *testing.T) {
	dir := t.TempDir()
	main.WriteManifest(t, dir, `databases:
  - database: orders
    app: api
    scope: production
    env: DATABASE_URL
`)
	fake := &driftManifestPostgresClient{
		fakeManifestPostgresClient: fakeManifestPostgresClient{
			app:       api.AppResponse{ID: "app-id", Slug: "api"},
			databases: api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-id", Name: "orders", State: "ready"}}},
			binding:   api.ManagedPostgresBinding{ID: "binding-id", State: "ready", EnvironmentKey: "DATABASE_URL"},
		},
		live: api.AppPostgresBindingList{Items: []api.AppPostgresBinding{
			{ID: "binding-id", Database: "orders", Scope: "production", EnvironmentKey: "DATABASE_URL", State: "ready"},
			{ID: "stale-id", Database: "legacy", Scope: "production", EnvironmentKey: "LEGACY_DATABASE_URL", State: "ready"},
			{ID: "preview-id", Database: "legacy", Scope: "preview", EnvironmentKey: "LEGACY_DATABASE_URL", State: "ready"},
		}},
	}
	stderr := &bytes.Buffer{}
	previous := *main.OsStderr
	*main.OsStderr = stderr
	t.Cleanup(func() { *main.OsStderr = previous })
	previousJSON := *main.JsonOutput
	*main.JsonOutput = true
	t.Cleanup(func() { *main.JsonOutput = previousJSON })
	if err := main.DeployManifestPostgresBindings(context.Background(), fake, "api", dir); err != nil {
		t.Fatalf("err = %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "LEGACY_DATABASE_URL (database legacy, scope production)") || !strings.Contains(out, "gregale postgres bindings delete stale-id") {
		t.Fatalf("missing drift warning: %q", out)
	}
	if strings.Contains(out, "preview-id") || strings.Contains(out, "delete binding-id") {
		t.Fatalf("warned about a declared or out-of-scope binding: %q", out)
	}
}
