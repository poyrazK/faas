//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestOutboundProviderCooldownMigrationDefaultsAndReplay(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	accountID, integrationID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, plan) VALUES ($1, $2, 'pro')`, accountID, "outbound-provider-cooldown-"+accountID.String()+"@example.com"); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbound_integrations
		    (id, account_id, name, origin, token_hash, rate_per_second, burst, max_in_flight,
		     request_timeout_ms, enabled, provider_auth_mode, allowed_methods,
		     allowed_path_prefixes, credential_source, owner_kind)
		VALUES ($1, $2, 'provider-cooldown', 'https://api.example.com', $3, 1, 1, 1,
		        1000, true, 'managed', ARRAY['GET']::text[], ARRAY['/v1']::text[], 'operator_env', 'operator')`,
		integrationID, accountID, make([]byte, 32)); err != nil {
		t.Fatalf("insert integration: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_admission_state (integration_id, tokens, last_refill) VALUES ($1, 1, now())`, integrationID); err != nil {
		t.Fatalf("insert admission state: %v", err)
	}
	var isNull bool
	var policyRevision int64
	if err := pool.QueryRow(ctx, `SELECT provider_cooldown_until IS NULL, provider_cooldown_policy_revision FROM outbound_admission_state WHERE integration_id = $1`, integrationID).Scan(&isNull, &policyRevision); err != nil {
		t.Fatalf("read cooldown default: %v", err)
	}
	if !isNull || policyRevision != 0 {
		t.Fatalf("provider cooldown defaults are null=%t revision=%d; want true/0", isNull, policyRevision)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_admission_state SET provider_cooldown_until = now() + interval '10 seconds', provider_cooldown_policy_revision = 42 WHERE integration_id = $1`, integrationID); err != nil {
		t.Fatalf("set cooldown: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = 20260927170719769`); err != nil {
		t.Fatalf("remove migration ledger row: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay provider-cooldown migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT provider_cooldown_until IS NOT NULL, provider_cooldown_policy_revision FROM outbound_admission_state WHERE integration_id = $1`, integrationID).Scan(&isNull, &policyRevision); err != nil || !isNull || policyRevision != 42 {
		t.Fatalf("cooldown after replay is null=%t revision=%d, err=%v; want preserved", isNull, policyRevision, err)
	}
}
