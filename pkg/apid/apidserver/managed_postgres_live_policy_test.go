package apidserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// managed_postgres_policy is hot: the runtime config manager hands each
// version to the provider registry, a value the registry refuses never
// becomes effective, and the write-time check refuses it up front.
func TestManagedPostgresPolicyAppliesWithoutRestart(t *testing.T) {
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{
		DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"},
		MaxDatabasesPerAccount: 3,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "compose",
			Settings: map[string]string{"database_name": "gregale"}}},
	}, func(string) string { return "" }, map[string]managedpostgres.Factory{
		"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return composeTestProvider{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := newRuntimeConfigManager(func(string) string { return "" })
	if err := m.bindManagedPostgresPolicy(registry); err != nil {
		t.Fatalf("binding the default policy: %v", err)
	}
	if registry.ProvisioningEnabled() || registry.MaxDatabasesPerAccount() != 3 {
		t.Fatal("the default policy changed the file's values")
	}

	open := json.RawMessage(`{"provisioning_enabled": true, "max_databases_per_account": 10, "fleet": {"max_databases": 80, "max_monthly_cost_millicents": 50000000}}`)
	if applied, err := m.applyVersion(runtimeConfigManagedPostgresPolicy, open, 1); err != nil || !applied {
		t.Fatalf("applyVersion = %v, %v", applied, err)
	}
	if !registry.ProvisioningEnabled() || registry.MaxDatabasesPerAccount() != 10 || registry.Fleet().MaxDatabases != 80 {
		t.Fatal("the live policy did not reach the registry")
	}

	unknownBackend := json.RawMessage(`{"production_approval": {"backend_id": "gone", "backend_fingerprint": "` + strings.Repeat("a", 64) +
		`", "approved_at": "2026-11-21T10:00:00Z", "until": "2026-11-28T10:00:00Z"}}`)
	if err := m.checkManagedPostgresPolicy(unknownBackend); err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("write-time check = %v, want unknown backend", err)
	}
	if _, err := m.applyVersion(runtimeConfigManagedPostgresPolicy, unknownBackend, 2); err == nil {
		t.Fatal("a policy the registry refuses was applied")
	}
	if string(m.Value(runtimeConfigManagedPostgresPolicy)) != string(open) || !registry.ProvisioningEnabled() {
		t.Fatal("a refused policy replaced the effective one")
	}
	def, _ := m.Definition(runtimeConfigManagedPostgresPolicy)
	if err := validateRuntimeConfigValue(def, json.RawMessage(`{"usage": {"enabled": false}}`)); err == nil {
		t.Fatal("usage metering is file-only, but the key accepted it")
	}
}
