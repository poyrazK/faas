package datapipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func assertNativeSchemaEvolution(t *testing.T, ctx context.Context, store *PostgresStore, engine *Engine, source, target *pgx.Conn, targetURL, reader, account string, tick func() api.DataPipeline, p api.DataPipeline) api.DataPipeline {
	t.Helper()
	old := startNativeDataAPI(t, ctx, targetURL, reader)
	defer old.close()
	originalGeneration := p.Generation
	name := pipelineName(Pipeline{DataPipeline: p})
	var tableOID, subOID uint32
	if err := target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid,(SELECT oid FROM pg_subscription WHERE subname=$1)`, name).Scan(&tableOID, &subOID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN extra_note text`); err != nil {
		t.Fatal(err)
	}
	p = tick()
	get := func() Pipeline {
		t.Helper()
		v, err := store.Get(ctx, account, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	pending := get()
	if p.Reason != "schema_change_pending" || pending.SchemaChange == nil || !pending.SchemaChange.Supported {
		t.Fatalf("missing supported preview: %+v", p)
	}
	first := pending.SchemaChange.ID
	if status, body := old.request("GET", "alice", "id=eq.1&select=id,total", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("pending schema exposed rows: %d %s", status, body)
	}
	var added bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='public.orders'::regclass AND attname='extra_note')`).Scan(&added); err != nil || added {
		t.Fatal("DDL applied before approval", err)
	}
	if err := store.ApproveSchema(ctx, account, p.ID, p.Revision, first, false); err != nil {
		t.Fatal(err)
	}
	// An additional change invalidates an approval, even before the first apply.
	if _, err := source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN also_note text; UPDATE public.orders SET extra_note='approved value',also_note='second value' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	p = tick()
	pending = get()
	if pending.SchemaChange == nil || pending.SchemaChange.ID == first || p.Reason != "schema_change_pending" {
		t.Fatal("approval covered a different definition")
	}
	if err := store.ApproveSchema(ctx, account, p.ID, p.Revision, first, false); !errors.Is(err, ErrConflict) {
		t.Fatal("stale approval accepted", err)
	}
	if err := store.ApproveSchema(ctx, account, p.ID, p.Revision, pending.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	// Losing the catalog lease before target commit must roll back every column
	// and the receipt, even though the approval itself is valid.
	approved := get()
	publisher, err := pgxpool.New(ctx, source.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	var schemaOwner string
	if err = target.QueryRow(ctx, `SELECT tableowner FROM pg_tables WHERE schemaname='public' AND tablename='orders'`).Scan(&schemaOwner); err != nil {
		t.Fatal(err)
	}
	err = applySchema(ctx, approved, publisher, target, schemaOwner, *approved.SchemaChange, func(context.Context) error { return ErrConflict })
	publisher.Close()
	if !errors.Is(err, ErrConflict) {
		t.Fatal("stale lease committed remote DDL", err)
	}
	if err = target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='public.orders'::regclass AND attname='extra_note')`).Scan(&added); err != nil || added {
		t.Fatal("stale lease left partial target DDL", err)
	}
	ready, refreshCalled, lateChange := false, false, false
	originalRefresh := engine.RefreshSchema
	defer func() { engine.RefreshSchema = originalRefresh }()
	engine.RefreshSchema = func(ctx context.Context, _ Pipeline, _ SchemaChange) ([]SchemaRefresh, bool, error) {
		refreshCalled = true
		if ready && !lateChange {
			lateChange = true
			_, err := source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN during_refresh text`)
			if err != nil {
				t.Fatal(err)
			}
		}
		return []SchemaRefresh{{Slug: "native-api", Status: "pending"}}, ready, nil
	}
	// Lose the remote DDL acknowledgement and recover through its durable receipt.
	originalTarget := engine.Target
	lostAck := false
	engine.Target = func(ctx context.Context, a, id string, run func(*pgx.Conn, string) error) error {
		return originalTarget(ctx, a, id, func(conn *pgx.Conn, role string) error {
			if err := run(conn, role); err != nil {
				return err
			}
			var change string
			if err := conn.QueryRow(ctx, `SELECT schema_change FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&change); err != nil {
				return err
			}
			if change != "" && !lostAck {
				lostAck = true
				return ErrUnavailable
			}
			return nil
		})
	}
	defer func() { engine.Target = originalTarget }()
	wait := func(reason string) {
		t.Helper()
		for until := time.Now().Add(20 * time.Second); ; {
			p = tick()
			if p.Reason == reason {
				return
			}
			if p.State == "resync_required" || time.Now().After(until) {
				t.Fatalf("waiting for %s: %+v", reason, p)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	wait("schema_api_refresh_pending")
	if !lostAck || !refreshCalled {
		t.Fatal("recovery/refresh paths were not exercised")
	}
	ready = true
	p = tick() // The source changes inside the refresh completion callback.
	if !lateChange || p.State == "live" {
		t.Fatal("source changed during refresh without closing gate")
	}
	wait("schema_change_pending")
	pending = get()
	if err := store.ApproveSchema(ctx, account, p.ID, p.Revision, pending.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	ready = false
	wait("schema_api_refresh_pending")
	old.close()
	fresh := startNativeDataAPI(t, ctx, targetURL, reader)
	defer fresh.close()
	if fresh.fingerprint == "" || fresh.fingerprint == old.fingerprint {
		t.Fatal("runtime contract did not refresh")
	}
	if status, body := fresh.request("GET", "alice", "id=eq.1&select=id,extra_note,also_note,during_refresh", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("reads reopened before refresh acknowledgement: %d %s", status, body)
	}
	ready = true
	for until := time.Now().Add(20 * time.Second); ; {
		p = tick()
		if p.State == "live" {
			break
		}
		if p.State == "resync_required" || time.Now().After(until) {
			t.Fatalf("approved schema did not converge: %+v", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status, body := fresh.request("GET", "alice", "id=eq.1&select=id,extra_note,also_note,during_refresh", ""); status != 200 || !strings.Contains(body, "approved value") || !strings.Contains(body, "second value") {
		t.Fatalf("new fields unavailable: %d %s", status, body)
	}
	if status, body := fresh.request("GET", "bob", "id=eq.1&select=id,extra_note", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("new column leaked rows: %d %s", status, body)
	}
	if status, _ := fresh.request("PATCH", "alice", "id=eq.1", `{"extra_note":"forbidden"}`); status != 403 {
		t.Fatal("evolved mirror became writable", status)
	}
	var same bool
	if err := target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid=$2 AND (SELECT oid=$3 AND subslotname=$1 FROM pg_subscription WHERE subname=$1)`, name, tableOID, subOID).Scan(&same); err != nil || !same {
		t.Fatal("schema evolution replaced replication or table", err)
	}
	saved := get()
	if saved.SchemaChange.State != "complete" || saved.Generation != originalGeneration {
		t.Fatal("schema evolution recopied the table")
	}
	// Reverting a later pending change must clear approval even when the
	// receipt still records an earlier completed migration.
	if _, err := source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN withdrawn text`); err != nil {
		t.Fatal(err)
	}
	wait("schema_change_pending")
	pending = get()
	if err := store.ApproveSchema(ctx, account, p.ID, p.Revision, pending.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `ALTER TABLE public.orders DROP COLUMN withdrawn`); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(10 * time.Second); ; {
		p = tick()
		if p.State == "live" {
			break
		}
		if p.State == "resync_required" || time.Now().After(until) {
			t.Fatalf("withdrawn source definition did not recover: %+v", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
	pending = get()
	if pending.SchemaApproval != "" || pending.SchemaChange.State != "withdrawn" {
		t.Fatal("reverted change kept approval")
	}
	// A removed historical default can leave rewritten values that no longer
	// appear in pg_attribute and never entered the logical change stream.
	if _, err := source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN historical text DEFAULT 'hidden historical value'; ALTER TABLE public.orders ALTER COLUMN historical DROP DEFAULT; ALTER TABLE public.orders ALTER COLUMN historical TYPE text USING historical || ''`); err != nil {
		t.Fatal(err)
	}
	wait("schema_change_pending")
	pending = get()
	if !pending.SchemaChange.Supported {
		t.Fatal("fixture did not clear historical default metadata")
	}
	if err := store.ApproveSchema(ctx, account, p.ID, p.Revision, pending.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(20 * time.Second); ; {
		p = tick()
		if p.State == "live" {
			break
		}
		if p.State == "resync_required" || time.Now().After(until) {
			t.Fatalf("historical default backfill failed: %+v", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var historical string
	if err := target.QueryRow(ctx, `SELECT historical FROM public.orders WHERE id=1`).Scan(&historical); err != nil || historical != "hidden historical value" || p.Generation != originalGeneration {
		t.Fatal("historical default was not backfilled in place", historical, err)
	}
	// A publication lost while approval is pending cannot be silently recreated.
	if _, err := source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN pending_lost text`); err != nil {
		t.Fatal(err)
	}
	wait("schema_change_pending")
	if _, err := source.Exec(ctx, "DROP PUBLICATION "+ident(pipelineName(Pipeline{DataPipeline: p}))); err != nil {
		t.Fatal(err)
	}
	wait("replication_state_lost")
	reset, err := store.Control(ctx, account, p.ID, p.Revision, "resync")
	if err != nil {
		t.Fatal(err)
	}
	p = reset.DataPipeline
	for until := time.Now().Add(20 * time.Second); ; {
		p = tick()
		if p.State == "live" {
			break
		}
		if p.State == "resync_required" || time.Now().After(until) {
			t.Fatalf("lost publication recovery failed: %+v", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return p
}
