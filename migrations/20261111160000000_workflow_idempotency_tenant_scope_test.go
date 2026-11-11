package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestWorkflowTenantIdempotencyMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `CREATE TABLE workflow_runs (
        id uuid PRIMARY KEY, app_id uuid NOT NULL, workflow_name text NOT NULL,
        platform_tenant_id uuid, create_idempotency_key text);
        CREATE UNIQUE INDEX workflow_runs_create_idempotency_idx
        ON workflow_runs (app_id,workflow_name,create_idempotency_key)
        WHERE create_idempotency_key IS NOT NULL;
        CREATE INDEX workflow_runs_create_tenant_idempotency_idx ON workflow_runs (app_id);`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		for _, migration := range []string{"20261111160000000_workflow_idempotency_tenant_scope.sql", "20261111160000001_workflow_idempotency_legacy_index.sql"} {
			raw, err := migrations.FS.ReadFile(migration)
			if err != nil {
				t.Fatal(err)
			}
			up := strings.SplitN(string(raw), "-- +goose Down", 2)[0]
			for _, statement := range strings.Split(up, ";") {
				if strings.TrimSpace(statement) == "" {
					continue
				}
				if _, err := pool.Exec(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	query := `INSERT INTO workflow_runs (id,app_id,workflow_name,platform_tenant_id,create_idempotency_key)
        VALUES (gen_random_uuid(),'00000000-0000-0000-0000-000000000001','test',NULLIF($1,'')::uuid,'same-key')`
	for _, tenant := range []string{"", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"} {
		if _, err := pool.Exec(ctx, query, tenant); err != nil {
			t.Fatal("distinct namespace rejected", err)
		}
		if _, err := pool.Exec(ctx, query, tenant); err == nil {
			t.Fatal("duplicate key accepted in the same namespace")
		}
	}
	// A refused downgrade leaves an invalid concurrent index behind. Once
	// the fixture's conflicting keys are reconciled, retry must rebuild it.
	raw, err := migrations.FS.ReadFile("20261111160000001_workflow_idempotency_legacy_index.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.SplitN(string(raw), "-- +goose Down", 2)[1]
	applyDown := func() error {
		for _, statement := range strings.Split(down, ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if _, err := pool.Exec(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	}
	if err := applyDown(); err == nil {
		t.Fatal("downgrade accepted conflicting tenant keys")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM workflow_runs WHERE platform_tenant_id IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if err := applyDown(); err != nil {
		t.Fatal("downgrade could not retry after key reconciliation", err)
	}
	if _, err := pool.Exec(ctx, query, ""); err == nil {
		t.Fatal("downgrade did not restore account-owned uniqueness")
	}
}
