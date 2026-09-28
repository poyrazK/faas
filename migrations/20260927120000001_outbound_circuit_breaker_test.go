//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestOutboundCircuitBreakerMigrationDefaultsAndBounds(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	accountID, integrationID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, plan) VALUES ($1, $2, 'pro')`, accountID, "outbound-circuit-"+accountID.String()+"@example.com"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbound_integrations
		    (id, account_id, name, origin, token_hash, rate_per_second, burst, max_in_flight,
		     request_timeout_ms, enabled, provider_auth_mode, allowed_methods,
		     allowed_path_prefixes, credential_source, owner_kind)
		VALUES ($1, $2, 'circuit-policy', 'https://api.example.com', $3, 1, 1, 1,
		        1000, true, 'managed', ARRAY['GET']::text[], ARRAY['/v1']::text[], 'operator_env', 'operator')`,
		integrationID, accountID, make([]byte, 32)); err != nil {
		t.Fatalf("insert legacy-shaped integration: %v", err)
	}
	var threshold, openSeconds int
	if err := pool.QueryRow(ctx, `SELECT circuit_breaker_failure_threshold, circuit_breaker_open_seconds FROM outbound_integrations WHERE id = $1`, integrationID).Scan(&threshold, &openSeconds); err != nil {
		t.Fatalf("read breaker defaults: %v", err)
	}
	if threshold != 0 || openSeconds != 0 {
		t.Fatalf("breaker defaults = %d/%d, want disabled pair 0/0", threshold, openSeconds)
	}
	for _, invalid := range [][2]int{{1, 0}, {0, 1}, {21, 30}, {3, 301}} {
		if _, err := pool.Exec(ctx, `UPDATE outbound_integrations SET circuit_breaker_failure_threshold = $2, circuit_breaker_open_seconds = $3 WHERE id = $1`, integrationID, invalid[0], invalid[1]); err == nil {
			t.Errorf("database accepted invalid breaker policy %d/%d", invalid[0], invalid[1])
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_integrations SET circuit_breaker_failure_threshold = 20, circuit_breaker_open_seconds = 300 WHERE id = $1`, integrationID); err != nil {
		t.Fatalf("database rejected maximum breaker policy: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_admission_state (integration_id, tokens, last_refill) VALUES ($1, 1, now())`, integrationID); err != nil {
		t.Fatalf("insert admission state with legacy shape: %v", err)
	}
	var stateThreshold, stateOpenSeconds, failures int
	if err := pool.QueryRow(ctx, `SELECT circuit_policy_failure_threshold, circuit_policy_open_seconds, circuit_failure_count FROM outbound_admission_state WHERE integration_id = $1`, integrationID).Scan(&stateThreshold, &stateOpenSeconds, &failures); err != nil {
		t.Fatalf("read admission-state defaults: %v", err)
	}
	if stateThreshold != 0 || stateOpenSeconds != 0 || failures != 0 {
		t.Fatalf("breaker state defaults = %d/%d/%d, want 0/0/0", stateThreshold, stateOpenSeconds, failures)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = 20260927120000001`); err != nil {
		t.Fatalf("remove migration ledger row: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay circuit-breaker migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT circuit_breaker_failure_threshold, circuit_breaker_open_seconds FROM outbound_integrations WHERE id = $1`, integrationID).Scan(&threshold, &openSeconds); err != nil || threshold != 20 || openSeconds != 300 {
		t.Fatalf("breaker policy after replay = %d/%d, err=%v; want 20/300", threshold, openSeconds, err)
	}
}
