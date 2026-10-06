//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// ADR-595: the additive migration starts disabled and can replay without
// changing existing recipient ownership, generation, lease, or retry counters.
func TestMigrations_EventRecipientClaimsReplayPreservesOwnership(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID, appID, subscriptionID, token := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,plan) VALUES ($1,$2,'pro')`, accountID, accountID+"@example.com"); err != nil {
		t.Fatal(err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx, `INSERT INTO event_fanout_outbox (account_id,source,event_id,event_type,event_data,payload)
		VALUES ($1,'orders','evt-migration','order.created','{}','{}') RETURNING id`, accountID).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	var adopted bool
	if err := pool.QueryRow(ctx, `SELECT recipient_claims FROM event_fanout_outbox WHERE id=$1`, outboxID).Scan(&adopted); err != nil || adopted {
		t.Fatalf("default adoption = %v, %v", adopted, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE event_fanout_outbox SET recipient_claims=true,state='processing' WHERE id=$1`, outboxID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO event_fanout_recipients
		(outbox_id,subscription_id,app_id,recipient,state,generation,attempts,total_attempts,claim_token,lease_until)
		VALUES ($1,$2,$3,'{}','processing',4,2,19,$4,now()+interval '5 minutes')`, outboxID, subscriptionID, appID, token); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=20261004191341419`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay migration: %v", err)
	}
	var actualToken string
	var generation int64
	var attempts, total int
	if err := pool.QueryRow(ctx, `SELECT claim_token::text,generation,attempts,total_attempts FROM event_fanout_recipients WHERE outbox_id=$1 AND subscription_id=$2`, outboxID, subscriptionID).Scan(&actualToken, &generation, &attempts, &total); err != nil {
		t.Fatal(err)
	}
	if actualToken != token || generation != 4 || attempts != 2 || total != 19 {
		t.Fatalf("ownership changed on migration replay: %s/%d/%d/%d", actualToken, generation, attempts, total)
	}
}
