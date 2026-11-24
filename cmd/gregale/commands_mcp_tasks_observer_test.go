package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPTasksObserverConfigEnvNames(t *testing.T) {
	config := main.McpTasksObserverSourceConfig{Tasks: &struct {
		Enabled        bool   `json:"enabled"`
		DatabaseURLEnv string `json:"database_url_env"`
		OwnerKeyEnv    string `json:"owner_key_env"`
	}{Enabled: true}}
	databaseEnv, ownerKeyEnv, err := main.McpTasksObserverConfigEnvNames(config)
	if err != nil {
		t.Fatal(err)
	}
	if databaseEnv != main.McpTasksObserverDatabaseEnvDefault || ownerKeyEnv != main.McpTasksObserverOwnerKeyEnvDefault {
		t.Fatalf("default env names = %q/%q", databaseEnv, ownerKeyEnv)
	}

	config.Tasks.DatabaseURLEnv = "TASKS_DATABASE_URL"
	config.Tasks.OwnerKeyEnv = "TASKS_OWNER_KEY"
	databaseEnv, ownerKeyEnv, err = main.McpTasksObserverConfigEnvNames(config)
	if err != nil {
		t.Fatal(err)
	}
	if databaseEnv != "TASKS_DATABASE_URL" || ownerKeyEnv != "TASKS_OWNER_KEY" {
		t.Fatalf("custom env names = %q/%q", databaseEnv, ownerKeyEnv)
	}
	config.Tasks.DatabaseURLEnv = "database-url"
	if _, _, err := main.McpTasksObserverConfigEnvNames(config); err == nil {
		t.Fatal("invalid lowercase database env name was accepted")
	}
}

func TestReadMCPTasksObserverSourceConfigRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gregale-mcp.json")
	if err := os.WriteFile(path, []byte(`{"tasks":{"enabled":true}} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := main.ReadMCPTasksObserverSourceConfig(path); err == nil {
		t.Fatal("config with trailing JSON was accepted")
	}
}

func TestMCPTasksObserverCredentialRequiresSecretReferenceAndExistingSecret(t *testing.T) {
	app := api.AppResponse{Manifest: api.AppManifest{
		Env:        map[string]string{"DATABASE_URL": "postgres://private-value"},
		EnvSecrets: map[string]string{"MCP_TASKS_SCALING_TOKEN": "secret:metrics-token"},
	}}
	metadata := api.AppSecretListResponse{SecretsByScope: api.SecretByScope{
		"production": {{Key: "metrics-token"}},
	}}

	database := main.McpTasksObserverCredential(app, "DATABASE_URL", metadata, true, nil)
	if !database.Configured || database.SecretBacked || database.Ready {
		t.Fatalf("plain database env should not be ready: %+v", database)
	}
	token := main.McpTasksObserverCredential(app, main.McpTasksObserverMetricsTokenEnv, metadata, true, nil)
	if !token.Configured || !token.SecretBacked || !token.SecretExists || !token.Ready {
		t.Fatalf("existing scoped token secret should be ready: %+v", token)
	}
	missing := main.McpTasksObserverCredential(app, main.McpTasksObserverMetricsTokenEnv, api.AppSecretListResponse{}, true, nil)
	if missing.SecretExists || missing.Ready {
		t.Fatalf("missing secret should not be ready: %+v", missing)
	}
	unverified := main.McpTasksObserverCredential(app, main.McpTasksObserverMetricsTokenEnv, api.AppSecretListResponse{}, false, nil)
	if unverified.SecretExists || unverified.Ready || unverified.MetadataLoaded {
		t.Fatalf("unavailable secret metadata should not be treated as ready: %+v", unverified)
	}
}

func TestMCPTasksObserverRequiresMatchingExplicitNamespaceAndWorkerSlug(t *testing.T) {
	worker := api.AppResponse{Slug: "mcp-worker", Manifest: api.AppManifest{Env: map[string]string{
		main.McpTasksObserverNamespaceEnv:   "tenant-queue",
		main.McpTasksObserverMetricsSlugEnv: "mcp-worker",
	}}}
	observer := api.AppResponse{Manifest: api.AppManifest{Env: map[string]string{
		main.McpTasksObserverNamespaceEnv:   " tenant-queue ",
		main.McpTasksObserverMetricsSlugEnv: "mcp-worker",
	}}}
	if !main.McpTasksObserverNamespacesMatch(worker, observer) {
		t.Fatal("matching explicit namespaces were rejected")
	}
	if !main.McpTasksObserverMetricsTargetMatches(observer, worker.Slug) {
		t.Fatal("observer metrics target matching the worker was rejected")
	}
	observer.Manifest.Env[main.McpTasksObserverNamespaceEnv] = "different-queue"
	if main.McpTasksObserverNamespacesMatch(worker, observer) {
		t.Fatal("mismatched namespaces were accepted")
	}
	observer.Manifest.Env[main.McpTasksObserverNamespaceEnv] = "tenant-queue"
	observer.Manifest.EnvSecrets = map[string]string{main.McpTasksObserverNamespaceEnv: "secret:queue-name"}
	if main.McpTasksObserverNamespacesMatch(worker, observer) {
		t.Fatal("secret-backed namespace that cannot be compared was accepted")
	}
	observer.Manifest.EnvSecrets = nil
	observer.Manifest.Env[main.McpTasksObserverMetricsSlugEnv] = "other-worker"
	if main.McpTasksObserverMetricsTargetMatches(observer, worker.Slug) {
		t.Fatal("observer metrics target mismatch was accepted")
	}
}

func TestMCPTasksObserverEntrypointDetection(t *testing.T) {
	if !main.McpTasksObserverEntrypointDetected([]string{"npm", "run", "start:tasks-observer"}) {
		t.Fatal("observer start script was not detected")
	}
	if !main.McpTasksObserverEntrypointDetected([]string{"node", "tasks-observer.js"}) {
		t.Fatal("observer source entrypoint was not detected")
	}
	if main.McpTasksObserverEntrypointDetected([]string{"node", "tasks-worker.js"}) {
		t.Fatal("worker entrypoint was classified as observer")
	}
}

// MCP Tasks prod probe 2026-11-22: the starter binds credentials with
// `gregale secrets set NAME` and managed PostgreSQL bindings, not secret
// references; both are ready.
func TestMCPTasksObserverCredentialAcceptsNamedSecretsAndPostgresBindings(t *testing.T) {
	app := api.AppResponse{}
	metadata := api.AppSecretListResponse{SecretsByScope: api.SecretByScope{
		"default": {{Key: main.McpTasksObserverMetricsTokenEnv}},
		"staging": {{Key: "MCP_TASK_OWNER_KEY"}},
	}}
	token := main.McpTasksObserverCredential(app, main.McpTasksObserverMetricsTokenEnv, metadata, true, nil)
	if !token.Configured || !token.SecretBacked || !token.SecretExists || !token.Ready {
		t.Fatalf("default-scope secret of the same name should be ready: %+v", token)
	}
	owner := main.McpTasksObserverCredential(app, "MCP_TASK_OWNER_KEY", metadata, true, nil)
	if owner.Ready {
		t.Fatalf("a secret in another scope is not what the deployed app reads: %+v", owner)
	}
	database := main.McpTasksObserverCredential(app, "DATABASE_URL", api.AppSecretListResponse{}, true, map[string]string{"DATABASE_URL": "read_only"})
	if !database.Ready || !database.ManagedPostgres {
		t.Fatalf("managed PostgreSQL binding should be ready: %+v", database)
	}
	unverified := main.McpTasksObserverCredential(app, main.McpTasksObserverMetricsTokenEnv, metadata, false, nil)
	if unverified.Ready {
		t.Fatalf("unloaded secret metadata should not be ready: %+v", unverified)
	}
}

// `gregale mcp tasks configure` writes runtime variables, which GET
// /v1/apps/{slug} does not carry; the checks read them over the manifest.
func TestMCPTasksRuntimeAppOverlaysRuntimeVariables(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/mcp-worker/env" || r.URL.Query().Get(api.EnvValuesQuery) != "true" {
			http.NotFound(w, r)
			return
		}
		value := "tenant-queue"
		_ = json.NewEncoder(w).Encode(api.AppEnvListResponse{Env: []api.AppEnvResponse{{Key: main.McpTasksObserverNamespaceEnv, Value: &value}}})
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "fp_live_x")
	worker := api.AppResponse{Slug: "mcp-worker", Manifest: api.AppManifest{Env: map[string]string{"NODE_ENV": "production"}}}
	got := main.McpTasksRuntimeApp(t.Context(), client, worker)
	if got.Manifest.Env[main.McpTasksObserverNamespaceEnv] != "tenant-queue" || got.Manifest.Env["NODE_ENV"] != "production" {
		t.Fatalf("env = %v, want runtime variables over the manifest env", got.Manifest.Env)
	}
	if _, leaked := worker.Manifest.Env[main.McpTasksObserverNamespaceEnv]; leaked {
		t.Fatal("overlay mutated the caller's manifest env")
	}
	other := api.AppResponse{Slug: "unreachable", Manifest: api.AppManifest{Env: map[string]string{"A": "1"}}}
	if got := main.McpTasksRuntimeApp(t.Context(), client, other); len(got.Manifest.Env) != 1 {
		t.Fatalf("a failed variable read changed the app: %v", got.Manifest.Env)
	}
}

// MCP Tasks prod re-probe 2026-11-23: a source-built observer has no entrypoint
// in GET /v1/apps, so setup reported observer_entrypoint_not_detected forever.
// The observer role detects it. The heartbeat plays no part: it cannot be
// attributed to the app, and an idle observer has none until --apply keeps it
// running.
func TestMCPTasksObserverProcessDetectedWithoutEntrypoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  api.AppResponse
		want bool
	}{
		{"role without entrypoint", api.AppResponse{Manifest: api.AppManifest{Env: map[string]string{"MCP_TASKS_ROLE": "observer"}}}, true},
		{"role with blank entrypoint", api.AppResponse{Manifest: api.AppManifest{Entrypoint: []string{" "}, Env: map[string]string{"MCP_TASKS_ROLE": "observer"}}}, true},
		{"role with known entrypoint", api.AppResponse{Manifest: api.AppManifest{Entrypoint: []string{"./server"}, Env: map[string]string{"MCP_TASKS_ROLE": "observer"}}}, true},
		{"other role", api.AppResponse{Manifest: api.AppManifest{Env: map[string]string{"MCP_TASKS_ROLE": "worker"}}}, false},
		{"entrypoint names the observer", api.AppResponse{Manifest: api.AppManifest{Entrypoint: []string{"node", "tasks-observer.js"}}}, true},
		{"other entrypoint without role", api.AppResponse{Manifest: api.AppManifest{Entrypoint: []string{"node", "server.js"}}}, false},
	} {
		if got := main.McpTasksObserverProcessDetected(tc.app); got != tc.want {
			t.Errorf("%s: detected = %v, want %v", tc.name, got, tc.want)
		}
	}
}
