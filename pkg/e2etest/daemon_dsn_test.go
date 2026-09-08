package e2etest

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestInjectDatabase(t *testing.T) {
	cases := []struct{ in, db, want string }{
		{"postgres://faas:faas@localhost:5432/faas?sslmode=disable", "clone_1", "postgres://faas:faas@localhost:5432/clone_1?sslmode=disable"},
		{"postgres:///faas?host=/run/postgresql&user=faas", "clone_1", "postgres:///clone_1?host=/run/postgresql&user=faas"},
		{"host=localhost dbname=faas user=faas", "clone_1", "host=localhost dbname=clone_1 user=faas"},
		{"host=localhost user=faas", "clone_1", "host=localhost user=faas dbname=clone_1"},
	}
	for _, c := range cases {
		if got := injectDatabase(c.in, c.db); got != c.want {
			t.Errorf("injectDatabase(%q, %q) = %q, want %q", c.in, c.db, got, c.want)
		}
	}
}

// TestDaemonDSN_FollowsPgtestIsolation proves the DSN handed to the daemon
// subprocesses lands in the same place as the test pool under both pgtest
// isolation modes: per-test schema (Open) and per-test cloned database
// (OpenMigrated with FAAS_PGTEST_TEMPLATE_DATABASE=1). It connects through
// the derived DSN exactly as a daemon would and checks where it ended up.
// Skips when no Postgres is reachable, like every pgtest-backed test.
func TestDaemonDSN_FollowsPgtestIsolation(t *testing.T) {
	// Reachability probe: pgtest.Open skips cleanly when no Postgres is
	// reachable (the pure-Go CI shards), whereas OpenMigrated's template
	// bootstrap fatals. Probe first so the whole test skips, not fails.
	pgtest.Open(t)
	base := baseDSN()

	t.Run("schema", func(t *testing.T) {
		pool := pgtest.Open(t)
		dsn := daemonDSN(base, pool)
		if !strings.Contains(dsn, "search_path="+pgtest.SchemaOf(pool)) {
			t.Fatalf("schema mode: dsn %q lacks search_path for %q", dsn, pgtest.SchemaOf(pool))
		}
		var gotSchema string
		queryVia(t, dsn, "select current_schema()", &gotSchema)
		if want := strings.TrimSuffix(pgtest.SchemaOf(pool), ",public"); gotSchema != want {
			t.Fatalf("schema mode: daemon DSN resolves current_schema()=%q, want %q", gotSchema, want)
		}
	})

	t.Run("template-clone", func(t *testing.T) {
		t.Setenv(pgtest.UseTemplateDatabase, "1")
		pool := pgtest.OpenMigrated(t)
		clone := pool.Config().ConnConfig.Database
		if !strings.HasPrefix(clone, "faas_test_clone_") {
			t.Fatalf("expected a template clone, pool database = %q", clone)
		}
		dsn := daemonDSN(base, pool)
		if strings.Contains(dsn, "search_path=") {
			t.Fatalf("clone mode: dsn %q must not carry a search_path", dsn)
		}
		var gotDB string
		queryVia(t, dsn, "select current_database()", &gotDB)
		if gotDB != clone {
			t.Fatalf("clone mode: daemon DSN resolves current_database()=%q, want %q", gotDB, clone)
		}
		// The clone is already migrated, which is the whole point: a daemon
		// booting against it has nothing to apply.
		var version int64
		queryVia(t, dsn, "select max(version_id) from goose_db_version", &version)
		if version < e2eMigrationTarget {
			t.Fatalf("clone mode: goose version %d < e2eMigrationTarget %d", version, e2eMigrationTarget)
		}
	})
}

func baseDSN() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return "postgres:///faas?host=/run/postgresql&user=faas"
}

func queryVia(t *testing.T, dsn, sql string, dst any) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open %q: %v", dsn, err)
	}
	defer pool.Close()
	if err := pool.QueryRow(context.Background(), sql).Scan(dst); err != nil {
		t.Fatalf("query %q via %q: %v", sql, dsn, err)
	}
}
