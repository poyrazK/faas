package migrations_test

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"os"
	"strings"
	"testing"
)

func TestMCPEnvironmentOAuthMigrationReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181314953_mcp_environment_oauth.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	for _, sql := range []string{parts[0], parts[1], parts[0], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}

	// An old worker must never process an unresolved scoped grant without scope.
	var account string
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email) VALUES('scope-rollback@example.com') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `WITH fixture AS (SELECT gen_random_uuid() AS id, gen_random_uuid() AS app)
 INSERT INTO mcp_oauth_retirements(id,account_id,app_id,binding_sha256,created_at,retain_until,next_attempt_at,payload)
 SELECT id,$1::uuid,app,repeat('a',64),now(),now()+interval '1 day',now(),jsonb_build_object('id',id,'app_id',app,'scope','staging','state','pending','history','[]'::jsonb,'revocation',jsonb_build_object('attempts',0)) FROM fixture`, account); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT scoped_retirement`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err == nil || !strings.Contains(err.Error(), "resolve environment OAuth retirements before rollback") {
		t.Fatal("rollback accepted unresolved scoped grant", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT scoped_retirement`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	// Replays must preserve future function definitions.
	if _, err = tx.Exec(ctx, `CREATE OR REPLACE FUNCTION mcp_readiness_subject(d deployments) RETURNS text LANGUAGE sql STABLE AS 'SELECT ''future-definition'''`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('mcp_readiness_subject(deployments)'::regprocedure)`).Scan(&definition); err != nil || !strings.Contains(definition, "future-definition") {
		t.Fatal("replay replaced later definition", err)
	}
}
