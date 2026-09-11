//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const (
	egressBillingFoundationPrevious int64 = 20260911212130123
	egressBillingFoundationVersion  int64 = 20260911213015602
)

func TestMigrations_EgressBillingFoundationIsMeterQualifiedAndRollingSafe(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, egressBillingFoundationPrevious)

	var accountID string
	if err := pool.QueryRow(ctx,
		`insert into accounts (email, plan) values ($1, 'pro') returning id::text`,
		"egress-foundation-"+uuid.NewString()+"@example.com").Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	hour := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	// This is the insert shape an older meterd binary uses during a rolling
	// deployment. The new columns must have compatible defaults.
	if _, err := pool.Exec(ctx,
		`insert into billing_usage_deliveries (provider, account_id, window_start, mb_seconds)
		 values ('polar', $1, $2, 321)`, accountID, hour); err != nil {
		t.Fatalf("seed legacy receipt: %v", err)
	}

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("apply egress billing foundation: %v", err)
	}
	assertEgressBillingFoundation(t, ctx, pool, accountID, hour)

	// Simulate schema-ahead/ledger-behind recovery. The migration must be
	// replayable without dropping either compute or egress receipts.
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id = $1`, egressBillingFoundationVersion); err != nil {
		t.Fatalf("delete migration ledger row: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay egress billing foundation: %v", err)
	}
	assertEgressBillingFoundation(t, ctx, pool, accountID, hour)
}

func assertEgressBillingFoundation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID string, hour time.Time) {
	t.Helper()
	var mbSeconds int64
	if err := pool.QueryRow(ctx,
		`select mb_seconds
		   from billing_usage_deliveries
		  where provider = 'polar' and account_id = $1 and window_start = $2`,
		accountID, hour).Scan(&mbSeconds); err != nil {
		t.Fatalf("read legacy compute receipt: %v", err)
	}
	if mbSeconds != 321 {
		t.Fatalf("legacy receipt MB-seconds = %d, want 321", mbSeconds)
	}
	// This is the exact conflict target used by the previous meterd binary. It
	// must remain valid after the migration while both binaries may be live.
	if _, err := pool.Exec(ctx,
		`insert into billing_usage_deliveries (provider, account_id, window_start, mb_seconds)
		 values ('polar', $1, $2, 999)
		 on conflict (provider, account_id, window_start) do nothing`, accountID, hour); err != nil {
		t.Fatalf("legacy rolling-deploy insert: %v", err)
	}
	var checkpointTable bool
	if err := pool.QueryRow(ctx,
		`select to_regclass(current_schema() || '.meter_network_checkpoints') is not null`).Scan(&checkpointTable); err != nil || !checkpointTable {
		t.Fatalf("checkpoint table = %t, err=%v", checkpointTable, err)
	}

	// Same provider/account/hour, different meter: both receipts coexist.
	var quantity int64
	if err := pool.QueryRow(ctx,
		`insert into billing_meter_usage_deliveries
		   (provider, account_id, meter, window_start, quantity)
		 values ('polar', $1, 'egress', $2, 456)
		 on conflict do nothing
		returning quantity`, accountID, hour).Scan(&quantity); err != nil {
		// ON CONFLICT on the replay pass returns no row, which is expected.
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("insert egress receipt: %v", err)
		}
		var count int
		if countErr := pool.QueryRow(ctx,
			`select count(*) from billing_meter_usage_deliveries
			  where provider = 'polar' and account_id = $1 and window_start = $2`,
			accountID, hour).Scan(&count); countErr != nil || count != 1 {
			t.Fatalf("meter-qualified receipts count=%d err=%v (insert err=%v), want 1", count, countErr, err)
		}
	} else if quantity != 456 {
		t.Fatalf("egress receipt bytes = %d, want 456", quantity)
	}
}
