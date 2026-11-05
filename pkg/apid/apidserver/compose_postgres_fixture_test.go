package apidserver

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// AttachTestManagedPostgres exposes attachTestManagedPostgres to the
// external apidserver_test package.
var AttachTestManagedPostgres = attachTestManagedPostgres

// ComposePostgresPlanFor plans a Compose `db: postgres:17` service that
// workload api depends on, and returns whether apply would provide it and
// the reason it would not.
func ComposePostgresPlanFor(srv *server, ctx context.Context, acct state.Account) (bool, string, error) {
	managed := []reposcan.Managed{{Name: "db", Kind: "postgres", EnvHint: "DATABASE_URL", Image: "postgres:17"}}
	workloads := []reposcan.Workload{{Name: "api", DependsOn: []string{"db"}}}
	provisions, reasons, err := srv.composePostgresPlan(ctx, acct, "company", "production", managed, workloads, reposcan.ComposeEnv{}, nil)
	_, provided := provisions["db"]
	return provided, reasons["db"], err
}

// ComposePostgresMajor exposes composePostgresMajor to the external
// apidserver_test package.
var ComposePostgresMajor = composePostgresMajor

// composeTestProvider is the source-ref test provider with point-in-time
// restore, which every production backend offers and the plan's default
// restore window needs.
type composeTestProvider struct {
	sourceRefManagedPostgresProvider
}

func (p composeTestProvider) Capabilities() managedpostgres.Capabilities {
	capabilities := p.sourceRefManagedPostgresProvider.Capabilities()
	capabilities.PointInTimeRestore = true
	return capabilities
}

// attachTestManagedPostgres gives srv a provisioning-enabled managed
// PostgreSQL service and binding service backed by the source-ref test
// provider, and returns their store so tests can inspect what apply created.
func attachTestManagedPostgres(t *testing.T, srv *server, maxDatabases int) *managedpostgres.MemoryStore {
	t.Helper()
	provider := composeTestProvider{}
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
