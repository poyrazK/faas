//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestOutboundRetryBudgetMigrationDefaultsBoundsAndReplay(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	accountID, integrationID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, plan) VALUES ($1, $2, 'pro')`, accountID, "outbound-retry-budget-"+accountID.String()+"@example.com"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbound_integrations
		    (id, account_id, name, origin, token_hash, rate_per_second, burst, max_in_flight,
		     request_timeout_ms, enabled, provider_auth_mode, allowed_methods,
		     allowed_path_prefixes, credential_source, owner_kind)
		VALUES ($1, $2, 'retry-budget', 'https://api.example.com', $3, 1, 1, 1,
		        1000, true, 'managed', ARRAY['GET']::text[], ARRAY['/v1']::text[], 'operator_env', 'operator')`,
		integrationID, accountID, make([]byte, 32)); err != nil {
		t.Fatalf("insert legacy-shaped integration: %v", err)
	}
	var budget int
	if err := pool.QueryRow(ctx, `SELECT retry_budget_per_minute FROM outbound_integrations WHERE id = $1`, integrationID).Scan(&budget); err != nil {
		t.Fatalf("read retry-budget default: %v", err)
	}
	if budget != 0 {
		t.Fatalf("retry-budget default = %d, want disabled zero", budget)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_integrations SET retry_budget_per_minute = 3001 WHERE id = $1`, integrationID); err == nil {
		t.Fatal("database accepted a retry budget above the global ceiling")
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_integrations SET retry_budget_per_minute = 3000 WHERE id = $1`, integrationID); err != nil {
		t.Fatalf("database rejected maximum retry budget: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_admission_state (integration_id, tokens, last_refill) VALUES ($1, 1, now())`, integrationID); err != nil {
		t.Fatalf("insert admission state with legacy shape: %v", err)
	}
	var retryTokens float64
	var retryBudgetRefilledAt time.Time
	var stateBudget int
	if err := pool.QueryRow(ctx, `SELECT retry_budget_tokens, retry_budget_refilled_at, retry_budget_policy_per_minute FROM outbound_admission_state WHERE integration_id = $1`, integrationID).Scan(&retryTokens, &retryBudgetRefilledAt, &stateBudget); err != nil {
		t.Fatalf("read admission-state defaults: %v", err)
	}
	if retryTokens != 0 || retryBudgetRefilledAt.IsZero() || stateBudget != 0 {
		t.Fatalf("retry-budget state defaults = tokens:%v refill:%v policy:%d, want 0/non-null/0", retryTokens, retryBudgetRefilledAt, stateBudget)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_admission_state SET retry_budget_tokens = 3001 WHERE integration_id = $1`, integrationID); err == nil {
		t.Fatal("database accepted retry tokens above the global ceiling")
	}

	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = 20260927161003477`); err != nil {
		t.Fatalf("remove migration ledger row: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay retry-budget migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT retry_budget_per_minute FROM outbound_integrations WHERE id = $1`, integrationID).Scan(&budget); err != nil || budget != 3000 {
		t.Fatalf("retry budget after replay = %d, err=%v; want 3000", budget, err)
	}
}
