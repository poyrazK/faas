package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r25: an app attached with `postgres attach --access read_write`
// ran CREATE TABLE IF NOT EXISTS at startup and got only "permission denied
// for schema public". `add postgres` names the migration binding; attach now
// does too.
func TestPostgresAttachReadWriteNamesTheMigrationBinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"web","type":"app","egress_ports":[5432]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"db-1","name":"orders","state":"ready"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
			_, _ = w.Write([]byte(`{"id":"b-1","database_id":"db-1","app_id":"app-1","scope":"default","environment_key":"DATABASE_URL","access":"read_write","state":"ready"}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOut(t)
	stdout, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"postgres", "attach", "orders", "web", "--env", "DATABASE_URL", "--access", "read_write"})
	restore()
	out := stdout.String()
	if code != 0 {
		t.Fatalf("exit %d\nstdout=%s\nstderr=%s", code, out, stderr())
	}
	if !strings.Contains(out, "Attached orders to app web as DATABASE_URL.") ||
		!strings.Contains(out, "gregale postgres attach orders web --scope default --access migration --env MIGRATION_DATABASE_URL") {
		t.Fatalf("attach receipt does not name the migration binding:\n%s", out)
	}
}
