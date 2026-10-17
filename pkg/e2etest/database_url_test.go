package e2etest

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
)

func TestDatabaseURLCloneAndFaultProxy(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	dsn := DatabaseURL(pool)
	proxy := postgresfixture.OpenProxy(t, pool, "127.0.0.1", 0)
	proxied, err := DatabaseURLViaTCP(pool, proxy.Host, proxy.Port)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{dsn, proxied} {
		p, err := pgxpool.New(t.Context(), candidate)
		if err != nil {
			t.Fatal(err)
		}
		var database, schema string
		err = p.QueryRow(t.Context(), "SELECT current_database(),current_schema()").Scan(&database, &schema)
		p.Close()
		if err != nil || database != pool.Config().ConnConfig.Database {
			t.Fatalf("daemon/proxy left clone: db=%s err=%v", database, err)
		}
	}
}

func TestDatabaseURLViaTCPKeepsActualDatabaseAndSchema(t *testing.T) {
	for _, dsn := range []string{"postgres://test@127.0.0.1/original?sslmode=disable", "host=127.0.0.1 user=test dbname=original sslmode=disable"} {
		for _, schema := range []string{"", "faas_test_cost,public"} {
			cfg, err := pgxpool.ParseConfig(dsn)
			if err != nil {
				t.Fatal(err)
			}
			cfg.ConnConfig.Database = "faas_test_clone_cost"
			if schema != "" {
				cfg.ConnConfig.RuntimeParams["search_path"] = schema
			}
			pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DatabaseURLViaTCP(pool, "127.0.0.1", 55433)
			pool.Close()
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := pgxpool.ParseConfig(got)
			if err != nil {
				t.Fatal(err)
			}
			wantDatabase := "faas_test_clone_cost"
			if parsed.ConnConfig.Database != wantDatabase || parsed.ConnConfig.RuntimeParams["search_path"] != schema || parsed.ConnConfig.Port != 55433 {
				t.Fatalf("isolation/transport changed: database=%s schema=%s port=%d", parsed.ConnConfig.Database, parsed.ConnConfig.RuntimeParams["search_path"], parsed.ConnConfig.Port)
			}
		}
	}
}
