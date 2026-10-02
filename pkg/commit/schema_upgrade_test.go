package commit

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPostgresOutboxManagedOperationsUpgrade(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	// Reproduce the published internal v1 delivery columns and unnamed check.
	legacy := strings.Replace(Schema, "    operation_id uuid,\n", "", 1)
	start := strings.Index(legacy, "    CONSTRAINT gregale_outbox_delivery_identity")
	end := strings.Index(legacy[start:], "\n);") + start
	legacy = legacy[:start] + `    CHECK ((accepted_at IS NULL AND receipt_id IS NULL AND invocation_id IS NULL)
        OR (accepted_at IS NOT NULL AND receipt_id IS NOT NULL AND invocation_id IS NOT NULL))` + legacy[end:]
	if _, err := pool.Exec(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	oldEvent, oldReceipt, invocation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_outbox(event_id,event_type,payload,accepted_at,receipt_id,invocation_id) VALUES($1,'legacy','{}',now(),$2,$3)`, oldEvent, oldReceipt, invocation); err != nil {
		t.Fatal(err)
	}
	if err := QualifySchema(ctx, pool); err == nil {
		t.Fatal("legacy schema was qualified for managed checkpoints")
	}
	for range 2 {
		if _, err := pool.Exec(ctx, UpgradeSchema); err != nil {
			t.Fatal(err)
		}
	}
	if err := QualifySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var retained string
	if err := pool.QueryRow(ctx, `SELECT invocation_id::text FROM gregale_outbox WHERE event_id=$1`, oldEvent).Scan(&retained); err != nil || retained != invocation {
		t.Fatalf("legacy identity=%s err=%v", retained, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_outbox(event_id,event_type,payload,accepted_at,receipt_id,operation_id) VALUES($1,'managed','{}',now(),$2,$3)`, uuid.NewString(), uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE gregale_outbox SET operation_id=$2 WHERE event_id=$1`, oldEvent, uuid.NewString()); err == nil {
		t.Fatal("accepted checkpoint acquired two work identities")
	}
}

func TestPostgresOutboxUpgradeRefusesUnknownConstraint(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE gregale_outbox(event_id uuid PRIMARY KEY,accepted_at timestamptz,receipt_id uuid,invocation_id uuid,CONSTRAINT gregale_outbox_check CHECK (receipt_id IS NULL))`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, UpgradeSchema); err == nil {
		t.Fatal("unknown delivery constraint was replaced")
	}
	// The explicit multi-statement transaction is aborted; release it before
	// inspecting the schema. No partial DDL may have escaped the owner upgrade.
	if _, err := pool.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	var added bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='gregale_outbox' AND column_name='operation_id')`).Scan(&added); err != nil || added {
		t.Fatalf("partial operation column=%t err=%v", added, err)
	}
}
