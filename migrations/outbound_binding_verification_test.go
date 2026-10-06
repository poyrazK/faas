//go:build !no_pg

// adr: 430 — rollback preserves customer catalogs and task history.
package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestOutboundBindingVerificationMigrationRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	acct := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, acct)
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO outbound_integrations(id,account_id,name,origin,token_hash,rate_per_second,burst,max_in_flight,request_timeout_ms,owner_kind,credential_source,provider_auth_mode,allowed_methods,allowed_path_prefixes) VALUES(gen_random_uuid(),$1,'probe','https://example.com',decode(repeat('00',32),'hex'),10,20,10,30000,'customer','customer_sealed','managed',ARRAY['GET'],ARRAY['/health']) RETURNING id`, acct).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_app_bindings(account_id,app_id,integration_id) VALUES($1,$2,$3)`, acct, app, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_integration_probe_policies VALUES($1,$2,'GET','/health',200)`, id, acct); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("20261002193101619_outbound_binding_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing down")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbound_app_bindings WHERE integration_id=$1)`, id).Scan(&exists); err != nil || !exists {
		t.Fatalf("rollback destroyed binding: %v", err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
}
