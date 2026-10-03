//go:build !no_pg

package migrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_PublishedEventCreatesDurableFanout(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,plan) VALUES ($1,$2,'pro')`,
		accountID, "event-fanout-"+accountID+"@example.com"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, accountID) })
	payload, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": "evt-1", "source": "orders", "type": "created",
		"time": "2026-09-27T00:00:00Z", "datacontenttype": "application/json",
		"accountid": accountID, "data": map[string]any{"amount": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	insert := func(data []byte) error {
		_, err := pool.Exec(ctx, `INSERT INTO events (actor,kind,subject,data) VALUES ('apid','event.published',$1::uuid,$2::jsonb)`, accountID, data)
		return err
	}
	if err := insert(payload); err != nil {
		t.Fatal(err)
	}
	if err := insert(payload); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM event_fanout_outbox WHERE account_id=$1::uuid`, accountID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("fanout receipts = %d, want 1", count)
	}
	var changed map[string]any
	if err := json.Unmarshal(payload, &changed); err != nil {
		t.Fatal(err)
	}
	changed["data"] = map[string]any{"amount": 2}
	conflictPayload, _ := json.Marshal(changed)
	err = insert(conflictPayload)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "event_fanout_identity_uniq" {
		t.Fatalf("changed identity error = %v, want event_fanout_identity_uniq", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM event_fanout_outbox WHERE account_id=$1::uuid`, accountID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("account deletion left %d fanout receipts", count)
	}
}

func TestMigrations_PublishedEventSnapshotsEligibleRecipients(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID, otherAccountID, appID, otherAppID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM event_subscriptions WHERE app_id IN ($1,$2)`, appID, otherAppID)
		_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id IN ($1,$2)`, appID, otherAppID)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id IN ($1,$2)`, accountID, otherAccountID)
	})
	for _, id := range []string{accountID, otherAccountID} {
		if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,plan) VALUES ($1,$2,'pro')`, id, "snapshot-"+id+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	for _, app := range []struct{ id, account string }{{appID, accountID}, {otherAppID, otherAccountID}} {
		if _, err := pool.Exec(ctx, `INSERT INTO apps (id,account_id,slug,ram_mb) VALUES ($1,$2,$3,256)`, app.id, app.account, "snapshot-"+app.id); err != nil {
			t.Fatal(err)
		}
	}
	var acceptedID string
	if err := pool.QueryRow(ctx, `INSERT INTO event_subscriptions (account_id,app_id,source,type,filter)
		VALUES ($1,$2,'orders.*','created','{"data":{"amount":{"$gt":100}}}'::jsonb) RETURNING id`, accountID, appID).Scan(&acceptedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO event_subscriptions (account_id,app_id,source,type) VALUES
		($1,$2,'orders.*','created'), ($3,$4,'orders.*','created'), ($1,$2,'invoices.*','created')`,
		accountID, appID, otherAccountID, otherAppID); err != nil {
		t.Fatal(err)
	}
	// Disable the second local candidate so only acceptedID remains.
	if _, err := pool.Exec(ctx, `UPDATE event_subscriptions SET enabled=false WHERE account_id=$1 AND filter='{}'::jsonb AND source='orders.*'`, accountID); err != nil {
		t.Fatal(err)
	}
	insert := func(id string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"specversion": "1.0", "id": id, "source": "orders.api",
			"type": "created", "accountid": accountID, "data": map[string]any{"amount": 150}})
		if _, err := pool.Exec(ctx, `INSERT INTO events (actor,kind,subject,data) VALUES ('apid','event.published',$1,$2::jsonb)`, accountID, payload); err != nil {
			t.Fatal(err)
		}
	}
	insert("first")
	var snapshot []byte
	if err := pool.QueryRow(ctx, `SELECT recipient_snapshot FROM event_fanout_outbox WHERE account_id=$1 AND event_id='first'`, accountID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var recipients []map[string]any
	if err := json.Unmarshal(snapshot, &recipients); err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 || recipients[0]["id"] != acceptedID || recipients[0]["filter"] == nil {
		t.Fatalf("captured recipients = %s, want original filtered subscription", snapshot)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM event_subscriptions WHERE id=$1`, acceptedID); err != nil {
		t.Fatal(err)
	}
	insert("first")
	var duplicateSnapshot []byte
	if err := pool.QueryRow(ctx, `SELECT recipient_snapshot FROM event_fanout_outbox WHERE account_id=$1 AND event_id='first'`, accountID).Scan(&duplicateSnapshot); err != nil {
		t.Fatal(err)
	}
	if string(duplicateSnapshot) != string(snapshot) {
		t.Fatalf("duplicate changed snapshot: before=%s after=%s", snapshot, duplicateSnapshot)
	}
	insert("second")
	if err := pool.QueryRow(ctx, `SELECT recipient_snapshot FROM event_fanout_outbox WHERE account_id=$1 AND event_id='second'`, accountID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if string(snapshot) != "[]" {
		t.Fatalf("new event recipients = %s, want []", snapshot)
	}
}
