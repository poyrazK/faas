//go:build !no_pg && !windows

package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// The parent owns the migrated database and credentials. This process owns only
// its HTTP listener and pool, so SIGKILL cannot destroy the recovery fixture.
func TestScenarioConsumerAcceptanceServer(t *testing.T) {
	// adr: 984 — a new API process must recover the original committed SQL state.
	if os.Getenv("GREGALE_CONSUMER_ACCEPTANCE_SERVER") != "1" {
		t.Skip("subprocess fixture only")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("acceptance requires PostgreSQL")
	}
	var input struct{ Database, Schema string }
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil || input.Database == "" || input.Schema == "" {
		t.Fatal("invalid fixture database selection")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid acceptance database config")
	}
	cfg.ConnConfig.Database = input.Database
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{input.Schema}.Sanitize() + ",public"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	srv := apidserver.NewServer(state.NewPgStore(pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).
		WithOpsMetrics(context.Background(), wire.NewOpsMetrics("apid_consumer_acceptance"))
	server := httptest.NewServer(srv.Handler())
	defer server.Close()
	var database, schema string
	if err := pool.QueryRow(context.Background(), `select current_database(),current_schema()`).Scan(&database, &schema); err != nil {
		t.Fatal(err)
	}
	ready := os.NewFile(3, "acceptance-ready")
	if ready == nil {
		t.Fatal("readiness pipe unavailable")
	}
	defer ready.Close()
	if err := json.NewEncoder(ready).Encode(struct {
		URL, Database, Schema string
		PID                   int
	}{server.URL, database, schema, os.Getpid()}); err != nil {
		t.Fatal("write readiness", err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}
