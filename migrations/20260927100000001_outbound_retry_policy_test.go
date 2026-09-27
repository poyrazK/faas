//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestOutboundRetryPolicyMigrationDefaultsAndBounds(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	accountID, integrationID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, plan) VALUES ($1, $2, 'pro')`, accountID, "outbound-retries-"+accountID.String()+"@example.com"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbound_integrations
		    (id, account_id, name, origin, token_hash, rate_per_second, burst, max_in_flight,
		     request_timeout_ms, enabled, provider_auth_mode, allowed_methods,
		     allowed_path_prefixes, credential_source, owner_kind)
		VALUES ($1, $2, 'retry-policy', 'https://api.example.com', $3, 1, 1, 1,
		        1000, true, 'managed', ARRAY['GET']::text[], ARRAY['/v1']::text[], 'operator_env', 'operator')`,
		integrationID, accountID, make([]byte, 32)); err != nil {
		t.Fatalf("insert legacy-shaped integration: %v", err)
	}
	var maxRetries int
	if err := pool.QueryRow(ctx, `SELECT max_retries FROM outbound_integrations WHERE id = $1`, integrationID).Scan(&maxRetries); err != nil {
		t.Fatalf("read retry default: %v", err)
	}
	if maxRetries != 0 {
		t.Fatalf("retry default = %d, want disabled (0)", maxRetries)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_integrations SET max_retries = 3 WHERE id = $1`, integrationID); err == nil {
		t.Fatal("database accepted a retry count above the hard cap")
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_integrations SET max_retries = 2 WHERE id = $1`, integrationID); err != nil {
		t.Fatalf("database rejected the maximum retry count: %v", err)
	}

	// Replaying the migration after a schema-ahead/ledger-behind event must
	// preserve the stored value and keep the constraint active.
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = 20260927100000001`); err != nil {
		t.Fatalf("remove migration ledger row: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay retry policy migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT max_retries FROM outbound_integrations WHERE id = $1`, integrationID).Scan(&maxRetries); err != nil || maxRetries != 2 {
		t.Fatalf("retry count after replay = %d, err=%v; want 2", maxRetries, err)
	}
}
