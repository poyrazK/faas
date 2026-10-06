package commit

import (
	"context"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
)

func TestPostgresSchemaQualificationRejectsMissingIdentity(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := context.Background()
	if err := QualifySchema(ctx, pool); err == nil {
		t.Fatal("missing outbox qualified")
	}
	if _, err := pool.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	if err := QualifySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE gregale_outbox DROP CONSTRAINT gregale_outbox_pkey`); err != nil {
		t.Fatal(err)
	}
	if err := QualifySchema(ctx, pool); err == nil {
		t.Fatal("outbox without stable identity qualified")
	}
}

func TestPostgresSourceBindingRejectsAnotherDestination(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	source := "00000000-0000-4000-8000-000000000001"
	other := "00000000-0000-4000-8000-000000000002"
	if err := QualifySource(ctx, pool, source); err == nil {
		t.Fatal("unbound outbox qualified")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO public.gregale_commit_binding(source_id) VALUES($1::uuid)`, source); err != nil {
		t.Fatal(err)
	}
	if err := QualifySource(ctx, pool, source); err != nil {
		t.Fatal(err)
	}
	if err := QualifySource(ctx, pool, other); err == nil {
		t.Fatal("another source qualified against the same database")
	}
	event := Event{ID: "00000000-0000-4000-8000-000000000003", Type: "order.created", Data: []byte(`{}`)}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, event); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	calls := 0
	relay := Relay{Pool: pool, SourceID: other, Acceptor: acceptFunc(func(context.Context, Event) (Receipt, error) {
		calls++
		return Receipt{ID: source, InvocationID: other}, nil
	})}
	if n, err := relay.Tick(ctx); err == nil || n != 0 || calls != 0 {
		t.Fatalf("wrong destination claimed work: %d %d %v", n, calls, err)
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT attempts FROM public.gregale_outbox WHERE event_id=$1::uuid`, event.ID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("wrong source mutated event: %d %v", attempts, err)
	}
	relay.SourceID = source
	if n, err := relay.Tick(ctx); err != nil || n != 1 || calls != 1 {
		t.Fatalf("bound source handoff: %d %d %v", n, calls, err)
	}
}
