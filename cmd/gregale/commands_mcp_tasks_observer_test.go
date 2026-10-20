package main_test

import (
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

	database := main.McpTasksObserverCredential(app, "DATABASE_URL", metadata, true)
	if !database.Configured || database.SecretBacked || database.Ready {
		t.Fatalf("plain database env should not be ready: %+v", database)
	}
	token := main.McpTasksObserverCredential(app, main.McpTasksObserverMetricsTokenEnv, metadata, true)
	if !token.Configured || !token.SecretBacked || !token.SecretExists || !token.Ready {
		t.Fatalf("existing scoped token secret should be ready: %+v", token)
	}
	missing := main.McpTasksObserverCredential(app, main.McpTasksObserverMetricsTokenEnv, api.AppSecretListResponse{}, true)
	if missing.SecretExists || missing.Ready {
		t.Fatalf("missing secret should not be ready: %+v", missing)
	}
	unverified := main.McpTasksObserverCredential(app, main.McpTasksObserverMetricsTokenEnv, api.AppSecretListResponse{}, false)
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
