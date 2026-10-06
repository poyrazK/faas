package commit_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	faas "github.com/poyrazK/faas/sdk/go"
)

func TestCommitSQLTransactionBoundary(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	database := "commit_sdk_go_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{database}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+quoted+" TEMPLATE template0 ENCODING 'UTF8'"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanCtx, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("drop private SDK database %s: %v", database, err)
		}
	}()
	cfg.Database = database
	cfg.RuntimeParams["search_path"] = "public"
	writer := stdlib.OpenDB(*cfg)
	defer writer.Close()
	observer := stdlib.OpenDB(*cfg)
	defer observer.Close()
	ddl, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "commit", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, string(ddl)+"; CREATE SCHEMA business; CREATE TABLE business.orders(id integer PRIMARY KEY); CREATE TABLE business.gregale_outbox(LIKE public.gregale_outbox INCLUDING ALL)"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	cfg.RuntimeParams["search_path"] = "business,public"
	writer = stdlib.OpenDB(*cfg)
	defer writer.Close()
	assertCounts := func(orders, events int) {
		t.Helper()
		var gotOrders, gotEvents int
		err := observer.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM business.orders),(SELECT count(*) FROM public.gregale_outbox)").Scan(&gotOrders, &gotEvents)
		if err != nil || gotOrders != orders || gotEvents != events {
			t.Fatalf("committed visibility orders=%d events=%d: %v", gotOrders, gotEvents, err)
		}
	}
	event := faas.CommitEventRequest{Type: "order.created", Data: json.RawMessage(`{"order_id":1}`)}
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, "INSERT INTO orders VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	identity, err := faas.InsertCommitEvent(ctx, tx, event)
	if err != nil {
		t.Fatal(err)
	}
	assertCounts(0, 0)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertCounts(1, 1)
	var gotID, kind string
	var orderID int
	if err := observer.QueryRowContext(ctx, "SELECT event_id::text,event_type,(payload->>'order_id')::int FROM public.gregale_outbox").Scan(&gotID, &kind, &orderID); err != nil || gotID != identity || kind != event.Type || orderID != 1 {
		t.Fatalf("event mismatch: %s %s %d %v", gotID, kind, orderID, err)
	}
	tx, err = writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO orders VALUES(2)"); err != nil {
		t.Fatal(err)
	}
	if _, err := faas.InsertCommitEvent(ctx, tx, event); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertCounts(1, 1)
	tx, err = writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO orders VALUES(3)"); err != nil {
		t.Fatal(err)
	}
	event.ID = identity
	_, err = faas.InsertCommitEvent(ctx, tx, event)
	var duplicate *pgconn.PgError
	if !errors.As(err, &duplicate) || duplicate.Code != "23505" {
		t.Fatalf("duplicate event identity: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertCounts(1, 1)
	tx, err = writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	event.ID = "not-an-event-id"
	if _, err := faas.InsertCommitEvent(ctx, tx, event); err == nil {
		t.Fatal("invalid event ID accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertCounts(1, 1)
	tx, err = writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	event.ID = ""
	event.Routing = &faas.CommitRouting{Version: 2, PlatformTenantID: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", Key: json.RawMessage(`"order-5"`)}
	if _, err := tx.ExecContext(ctx, "INSERT INTO orders VALUES(5)"); err != nil {
		t.Fatal(err)
	}
	routed, err := faas.InsertCommitEvent(ctx, tx, event)
	if err != nil {
		t.Fatal(err)
	}
	assertCounts(1, 1)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertCounts(2, 2)
	var tenant, key string
	if err := observer.QueryRowContext(ctx, "SELECT routing->>'platform_tenant_id',routing->>'key' FROM public.gregale_outbox WHERE event_id=$1::uuid", routed).Scan(&tenant, &key); err != nil || tenant != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" || key != "order-5" {
		t.Fatalf("routing lost: %s %s %v", tenant, key, err)
	}
	tx, err = writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := faas.InsertCommitEvent(ctx, tx, event); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertCounts(2, 2)

}
