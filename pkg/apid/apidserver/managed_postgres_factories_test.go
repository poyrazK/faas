//go:build !managed_postgres_native || !metal

package apidserver

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance configuration must fail before constructing a credential client
// in every production build, including ordinary metal binaries.
func TestManagedPostgresProductionRejectsFixtureDriver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-provider.json")
	config := `{"default_region":"us-east-1","defaults":{"us-east-1":"native-sql"},"backends":[{"id":"native-sql","driver":"sqlfixture","region":"us-east-1","namespace":"disposable","settings":{"control_url":"http://127.0.0.1:1"},"secret_env":{"control-token":"TEST_FIXTURE_TOKEN"}}]}`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, _, err := loadManagedPostgres(nil, func(name string) string {
		if name == "FAAS_MANAGED_POSTGRES_CONFIG" {
			return path
		}
		return "test-token"
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown driver sqlfixture") {
		t.Fatalf("production admitted test provider: %v", err)
	}
}
