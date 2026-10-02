//go:build !no_pg

package migrations_test

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationAppUDPListeners(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)

	var tableCount int
	if err := pool.QueryRow(t.Context(), `
		select count(*) from information_schema.tables
		 where table_schema = current_schema() and table_name = 'app_udp_listeners'`).Scan(&tableCount); err != nil {
		t.Fatalf("query table: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("table count=%d, want 1", tableCount)
	}
	var indexCount int
	if err := pool.QueryRow(t.Context(), `
		select count(*) from pg_indexes
		 where schemaname = current_schema()
		   and tablename = 'app_udp_listeners'
		   and indexname like 'app_udp_listeners_%'
		   and indexname <> 'app_udp_listeners_pkey'`).Scan(&indexCount); err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	if indexCount != 4 {
		t.Fatalf("index count=%d, want 4", indexCount)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "udp-migration@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "udp-migration", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	var enabled bool
	if err := pool.QueryRow(t.Context(), `insert into app_udp_listeners(account_id,app_id,listener_name,guest_port,public_port) values($1,$2,'dns',5353,40129) returning enabled`, account.ID, app.ID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("SQL default exposed a newly created listener")
	}
	for _, test := range []struct {
		name, protocol string
		guest, public  int
	}{
		{"bad-name!", "udp", 5353, 40130}, {"valid", "tcp", 5353, 40130}, {"valid", "udp", 0, 40130}, {"valid", "udp", 5353, 39999},
	} {
		_, err := pool.Exec(t.Context(), `insert into app_udp_listeners(account_id,app_id,listener_name,guest_port,public_port,protocol) values($1,$2,$3,$4,$5,$6)`, account.ID, app.ID, test.name, test.guest, test.public, test.protocol)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("constraint case %+v: %v", test, err)
		}
	}

}
