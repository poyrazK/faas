//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestOutboundResponseCacheMigrationDefaultsAndBounds(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	accountID, integrationID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, plan) VALUES ($1, $2, 'pro')`, accountID, "outbound-cache-"+accountID.String()+"@example.com"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbound_integrations
		    (id, account_id, name, origin, token_hash, rate_per_second, burst, max_in_flight,
		     request_timeout_ms, enabled, provider_auth_mode, allowed_methods,
		     allowed_path_prefixes, credential_source, owner_kind)
		VALUES ($1, $2, 'response-cache', 'https://api.example.com', $3, 1, 1, 1,
		        1000, true, 'managed', ARRAY['GET']::text[], ARRAY['/v1']::text[], 'operator_env', 'operator')`,
		integrationID, accountID, make([]byte, 32)); err != nil {
		t.Fatalf("insert legacy-shaped integration: %v", err)
	}
	var ttl int
	if err := pool.QueryRow(ctx, `SELECT response_cache_ttl_seconds FROM outbound_integrations WHERE id = $1`, integrationID).Scan(&ttl); err != nil {
		t.Fatalf("read cache default: %v", err)
	}
	if ttl != 0 {
		t.Fatalf("cache TTL default = %d, want disabled (0)", ttl)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_integrations SET response_cache_ttl_seconds = 301 WHERE id = $1`, integrationID); err == nil {
		t.Fatal("database accepted a cache TTL above the hard cap")
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_integrations SET response_cache_ttl_seconds = 300 WHERE id = $1`, integrationID); err != nil {
		t.Fatalf("database rejected maximum cache TTL: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = 20260927110000001`); err != nil {
		t.Fatalf("remove migration ledger row: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay response cache migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT response_cache_ttl_seconds FROM outbound_integrations WHERE id = $1`, integrationID).Scan(&ttl); err != nil || ttl != 300 {
		t.Fatalf("cache TTL after replay = %d, err=%v; want 300", ttl, err)
	}
}
