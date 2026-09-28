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
	ok := errors.As(err, &pgErr)
	if !ok || pgErr.Code != "23505" || pgErr.ConstraintName != "event_fanout_identity_uniq" {
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
