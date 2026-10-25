package apidserver

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// AttachTestManagedPostgres exposes attachTestManagedPostgres to the
// external apidserver_test package.
var AttachTestManagedPostgres = attachTestManagedPostgres

// attachTestManagedPostgres gives srv a provisioning-enabled managed
// PostgreSQL service and binding service backed by the source-ref test
// provider, and returns their store so tests can inspect what apply created.
func attachTestManagedPostgres(t *testing.T, srv *server, maxDatabases int) *managedpostgres.MemoryStore {
	t.Helper()
	provider := sourceRefManagedPostgresProvider{}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{
		DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"},
		MaxDatabasesPerAccount: maxDatabases, ProvisioningEnabled: true,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "compose",
			Settings: map[string]string{"database_name": "gregale"}}},
	}, func(string) string { return "" }, map[string]managedpostgres.Factory{
		"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return provider, nil
		},
	})
	if err != nil {
		t.Fatalf("managed postgres registry: %v", err)
	}
	store := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatalf("managed postgres service: %v", err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, store, store, &sourceRefManagedPostgresSink{puts: map[string]string{}},
		managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatalf("managed postgres binding service: %v", err)
	}
	srv.WithManagedPostgres(service, nil, bindings, nil, nil, nil)
	return store
}
