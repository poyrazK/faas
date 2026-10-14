// adr: 1027
package firecracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// The backend configuration ADR-1027 documents loads through the registry
// apid uses: its secret_env key passes the registry's naming rule and the
// factory finds the credential key through it. Nothing is dialled yet.
func TestDocumentedBackendLoadsThroughTheRegistry(t *testing.T) {
	config := `{"default_region": "eu-central", "defaults": {"eu-central": "native-eu"}, "max_databases_per_account": 3,
  "usage": {"enabled": false, "collection_interval_seconds": 300, "window_seconds": 3600, "stale_after_seconds": 10800},
  "backends": [{"id": "native-eu", "driver": "firecracker", "region": "eu-central", "namespace": "native",
    "settings": {"host_suffix": "db.gregale.dev", "schedd_target": "unix:///run/faas/schedd.sock", "max_restore_window_seconds": "604800"},
    "secret_env": {"credential-key": "FAAS_MANAGED_POSTGRES_NATIVE_CREDENTIAL_KEY"}}]}`
	path := filepath.Join(t.TempDir(), "managed-postgres.json")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), "postgres://gregale@127.0.0.1:1/gregale?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	env := map[string]string{
		"FAAS_MANAGED_POSTGRES_CONFIG":                path,
		"FAAS_MANAGED_POSTGRES_NATIVE_CREDENTIAL_KEY": strings.Repeat("k", MinCredentialKeyBytes),
	}
	registry, err := managedpostgres.Load(func(k string) string { return env[k] }, map[string]managedpostgres.Factory{Driver: Factory(pool)})
	if err != nil {
		t.Fatalf("load the documented native backend: %v", err)
	}
	backend, err := registry.Default("eu-central")
	if err != nil || backend.Driver != Driver {
		t.Fatalf("default backend = %+v, %v; want the native backend", backend, err)
	}
	delete(env, "FAAS_MANAGED_POSTGRES_NATIVE_CREDENTIAL_KEY")
	if _, err := managedpostgres.Load(func(k string) string { return env[k] }, map[string]managedpostgres.Factory{Driver: Factory(pool)}); err == nil {
		t.Fatal("loaded a native backend without its credential key")
	}
}
