package datarepair

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// Only private fixture metadata is backdated; source row identities and versions
// are never reset. Production has no bypass/force-retention switch.
func ageRetentionFixture(t *testing.T, e *Engine) {
	t.Helper()
	_, err := e.Pool.Exec(t.Context(), `BEGIN;
ALTER TABLE gregale_repair.operations DISABLE TRIGGER gregale_repair_protect_operation;
UPDATE gregale_repair.operations SET created_at=clock_timestamp()-interval '10 days';
ALTER TABLE gregale_repair.operations ENABLE TRIGGER gregale_repair_protect_operation;
UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '10 days',expires_at=clock_timestamp()-interval '9 days',
 applied_at=CASE WHEN applied_at IS NULL THEN NULL ELSE clock_timestamp()-interval '8 days' END;
COMMIT;`)
	if err != nil {
		t.Fatal(err)
	}
}

func retentionPreview(t *testing.T, e *Engine, s Scope, limit int) RetentionPlan {
	t.Helper()
	p, err := e.PlanRetention(t.Context(), s, time.Time{}, limit)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func retentionPurge(t *testing.T, e *Engine, s Scope, p RetentionPlan) RetentionPlan {
	t.Helper()
	out, err := e.PurgeRetention(t.Context(), s, p.ID, p.Digest, "retention-operator")
	if err != nil || out.AppliedAt == nil || out.AppliedBy == nil || *out.AppliedBy != "retention-operator" || out.AppliedRole == nil {
		t.Fatalf("purge receipt: %+v %v", out, err)
	}
	return out
}

func TestRepairRetentionReceiptsSurvivePayloadPurge(t *testing.T) {
	e, s, source := fixture(t, 500)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	p := approved(t, e, s, source)
	repairID := uuid.NewString()
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, repairID, "repair-v1"); err != nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
	if len(batch.Entries) != 1 || batch.Entries[0].Kind != "plan" || batch.Entries[0].ID != p.ID || batch.Entries[0].ChangeRows != 500 || batch.EstimatedPayloadBytes < 1 {
		t.Fatalf("unexpected first inventory: %+v", batch)
	}
	b, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{`"before"`, `"after"`, `"category"`, `"original"`, `"human"`} {
		// The inventory's cutoff is named before; executable field patches are
		// identified by object values, rather than that harmless timestamp key.
		if secret == `"before"` {
			secret = `"before":{`
		}
		if strings.Contains(string(b), secret) {
			t.Fatalf("payload leaked in metadata inventory: %s", secret)
		}
	}
	first := retentionPurge(t, e, s, batch)
	retained, err := e.Get(ctx, s, p.ID)
	if err != nil || retained.PayloadPurgedAt == nil || len(retained.Items) != 0 || retained.Digest != p.Digest || retained.Candidates != 460 || retained.Conflicts != 40 || retained.RepairOperationID == nil || *retained.RepairOperationID != repairID {
		t.Fatalf("lost compact receipt: %+v %v", retained, err)
	}
	// Source and inverse journals survive until their plan has been compacted.
	var rows int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM gregale_repair.changes`).Scan(&rows); err != nil || rows != 960 {
		t.Fatalf("source payload removed in the plan batch: %d %v", rows, err)
	}
	for range 2 {
		next := retentionPreview(t, e, s, 1)
		if len(next.Entries) != 1 || next.Entries[0].Kind != "operation" {
			t.Fatalf("bounded journal inventory: %+v", next)
		}
		retentionPurge(t, e, s, next)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM gregale_repair.changes`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("journals not purged: %d %v", rows, err)
	}
	if _, err := e.Prepare(ctx, s, source, uuid.NewString()); !errors.Is(err, ErrPurged) {
		t.Fatalf("new inverse plan synthesized after purge: %v", err)
	}
	if _, err := e.Prepare(ctx, s, source, p.ID); err != nil {
		t.Fatalf("retained plan identity no longer readable: %v", err)
	}
	// A lost response is reconciled even after payload purge and plan expiry,
	// with a changed live database/check. Another repair ID remains forbidden.
	e.CheckSQL = "SELECT false"
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET quantity=quantity+1`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, repairID, "later-release"); err != nil {
		t.Fatalf("receipt replay reran writes/checks: %v", err)
	}
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, uuid.NewString(), "later-release"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed repair ID accepted: %v", err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE gregale_repair.retention_runs SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, batch.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := e.PurgeRetention(ctx, s, batch.ID, batch.Digest, "another-operator")
	if err != nil || replay.AppliedAt == nil || !replay.AppliedAt.Equal(*first.AppliedAt) || *replay.AppliedBy != *first.AppliedBy {
		t.Fatalf("purge receipt replay changed: %+v %v", replay, err)
	}
	var original, human, quantity, operations, plans int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='original'),count(*) FILTER(WHERE category='human'),min(quantity),
 (SELECT count(*) FROM gregale_repair.operations),(SELECT count(*) FROM gregale_repair.plans) FROM products`).Scan(&original, &human, &quantity, &operations, &plans); err != nil || original != 460 || human != 40 || quantity != 2 || operations != 2 || plans != 1 {
		t.Fatalf("business state or identities changed: %d/%d quantity=%d operations=%d plans=%d %v", original, human, quantity, operations, plans, err)
	}
	if len(retentionPreview(t, e, s, 1).Entries) != 0 {
		t.Fatal("purged payload reappeared in a later inventory")
	}
	// Compaction preserves the content check for the retained metadata too.
	if _, err := e.Pool.Exec(ctx, `UPDATE gregale_repair.plans SET document=jsonb_set(document,'{candidates}','999') WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, repairID, "later-release"); !errors.Is(err, ErrConflict) {
		t.Fatalf("tampered compact receipt accepted: %v", err)
	}
}

func TestRepairRetentionHoldsUnknownAndYoungEvidence(t *testing.T) {
	e, s, source := fixture(t, 3)
	ctx := t.Context()
	if len(retentionPreview(t, e, s, 1).Entries) != 0 {
		t.Fatal("young source selected")
	}
	for _, limit := range []int{0, api.DataRepairPurgeMaxItems + 1} {
		if _, err := e.PlanRetention(ctx, s, time.Time{}, limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid bound accepted: %v", err)
		}
	}
	if _, err := e.PlanRetention(ctx, s, time.Now(), 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("shortened retention window: %v", err)
	}
	p := approved(t, e, s, source)
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
	if len(batch.Entries) != 0 || batch.HeldApprovedPlans != 1 || retentionHoldCount(batch, "plan", "unresolved_apply") != 1 {
		t.Fatalf("expired approval lost its uncertainty hold: %+v", batch)
	}
	retentionPurge(t, e, s, batch)
	got, err := e.Get(ctx, s, p.ID)
	if err != nil || len(got.Items) != 3 || got.PayloadPurgedAt != nil {
		t.Fatalf("unresolved plan payload changed: %+v %v", got, err)
	}
	other := s
	other.PlatformTenantID = uuid.NewString()
	if len(retentionPreview(t, e, other, 1).Entries) != 0 {
		t.Fatal("inventory crossed tenant scope")
	}
	if _, err := e.GetRetention(ctx, other, batch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-scope receipt read: %v", err)
	}
	if _, err := e.PurgeRetention(ctx, other, batch.ID, batch.Digest, "operator"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-scope purge: %v", err)
	}
}

func TestRepairRetentionExpiredUnapprovedPlan(t *testing.T) {
	e, s, source := fixture(t, 2)
	ctx := t.Context()
	p, err := e.Plan(ctx, s, source)
	if err != nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, 1)
	if len(batch.Entries) != 1 || batch.Entries[0].Kind != "plan" || batch.Entries[0].Reason != "expired_unapproved" {
		t.Fatalf("expired plan was not selected: %+v", batch)
	}
	retentionPurge(t, e, s, batch)
	if _, err := e.Approve(ctx, s, p.ID, p.Digest, "operator"); !errors.Is(err, ErrPurged) {
		t.Fatalf("purged plan accepted approval: %v", err)
	}
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, uuid.NewString(), "repair"); !errors.Is(err, ErrPurged) {
		t.Fatalf("purged unapproved plan accepted execution: %v", err)
	}
	retentionPurge(t, e, s, retentionPreview(t, e, s, 1))
	if _, err := e.Plan(ctx, s, source); !errors.Is(err, ErrPurged) {
		t.Fatalf("purged source accepted a new plan: %v", err)
	}
}

func TestRepairRetentionDoesNotRebaselineCorruptPlans(t *testing.T) {
	e, s, source := fixture(t, 1)
	ctx := t.Context()
	p := approved(t, e, s, source)
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, uuid.NewString(), "repair"); err != nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, 1)
	if _, err := e.Pool.Exec(ctx, `UPDATE gregale_repair.plans SET document=jsonb_set(document,'{candidates}','999') WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PurgeRetention(ctx, s, batch.ID, batch.Digest, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("corrupt plan was compacted: %v", err)
	}
	if _, err := e.PlanRetention(ctx, s, time.Time{}, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("fresh inventory legitimized corrupt receipt metadata: %v", err)
	}
}

func TestRepairRetentionRechecksWholeFrozenBatch(t *testing.T) {
	e, s, source := fixture(t, 2)
	ctx := t.Context()
	second := uuid.NewString()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Capture(ctx, tx, s, second, "import-two", "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE products SET category='original' WHERE id=1`)
		return err
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
	if len(batch.Entries) != 2 {
		t.Fatalf("expected two old captures: %+v", batch)
	}
	if _, err := e.PurgeRetention(ctx, s, batch.ID, strings.Repeat("0", 64), "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong inventory digest accepted: %v", err)
	}
	// A new plan after review pins one source; the entire batch must abort.
	if _, err := e.Plan(ctx, s, source); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PurgeRetention(ctx, s, batch.ID, batch.Digest, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale inventory accepted: %v", err)
	}
	var rows, purged int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM gregale_repair.changes),count(*) FILTER(WHERE payload_purged_at IS NOT NULL) FROM gregale_repair.operations`).Scan(&rows, &purged); err != nil || rows != 3 || purged != 0 {
		t.Fatalf("partial purge committed: rows=%d purged=%d %v", rows, purged, err)
	}
	next := retentionPreview(t, e, s, 1)
	if len(next.Entries) != 1 || next.Entries[0].ID != second {
		t.Fatalf("new selection ignored the pin: %+v", next)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE gregale_repair.retention_runs SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, next.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PurgeRetention(ctx, s, next.ID, next.Digest, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired review applied: %v", err)
	}
}

func TestRepairRetentionWaitsForRecordedTransaction(t *testing.T) {
	e, s, _ := fixture(t, 1)
	ctx := t.Context()
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, 1)
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := Capture(ctx, tx, s, uuid.NewString(), "concurrent-import", "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE products SET category='original'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := e.PurgeRetention(wait, s, batch.ID, batch.Digest, "operator"); err == nil {
		t.Fatal("purge raced an open capture transaction")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	retentionPurge(t, e, s, batch)
}

func TestRepairRetentionBoundsDoNotBroadenPurge(t *testing.T) {
	for _, bytesBound := range []bool{false, true} {
		name := "entry_count"
		if bytesBound {
			name = "logical_bytes"
		}
		t.Run(name, func(t *testing.T) {
			e, s, _ := fixture(t, 1)
			ctx := t.Context()
			target, count := "products", api.DataRepairPurgeMaxItems+2
			chunk := api.DataRepairMaxJournalBytes/2 - 128
			if bytesBound {
				target, count = "payloads", api.DataRepairPurgeMaxBytes/api.DataRepairMaxJournalBytes+2
				if _, err := e.Pool.Exec(ctx, `CREATE TABLE payloads(id int PRIMARY KEY,value text NOT NULL);
SELECT gregale_repair.enroll('payloads',ARRAY['value']);`); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Pool.Exec(ctx, `INSERT INTO payloads VALUES(1,repeat('x',$1))`, chunk); err != nil {
					t.Fatal(err)
				}
			}
			for range count {
				tx, err := e.Pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				err = Capture(ctx, tx, s, uuid.NewString(), "retention-bound", target, func(tx pgx.Tx) error {
					if bytesBound {
						_, err := tx.Exec(ctx, `UPDATE payloads SET value=CASE WHEN left(value,1)='x' THEN repeat('y',$1) ELSE repeat('x',$1) END`, chunk)
						return err
					}
					_, err := tx.Exec(ctx, `UPDATE products SET category=CASE WHEN category='bad' THEN 'human' ELSE 'bad' END`)
					return err
				})
				if err != nil {
					_ = tx.Rollback(ctx)
					t.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			ageRetentionFixture(t, e)
			batch := retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
			if len(batch.Entries) == 0 || len(batch.Entries) >= count+1 || len(batch.Entries) > api.DataRepairPurgeMaxItems || batch.EstimatedPayloadBytes > api.DataRepairPurgeMaxBytes {
				t.Fatalf("unbounded inventory: %+v", batch)
			}
			if !bytesBound && len(batch.Entries) != api.DataRepairPurgeMaxItems {
				t.Fatal("entry cap did not select the expected bounded batch")
			}
			retentionPurge(t, e, s, batch)
			var remaining int
			if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM gregale_repair.changes`).Scan(&remaining); err != nil || remaining != count+1-len(batch.Entries) {
				t.Fatalf("purge escaped reviewed bounds: remaining=%d err=%v", remaining, err)
			}
		})
	}
}

func TestRepairRetentionOwnerUpgradePreservesEnrollment(t *testing.T) {
	e, s, source := fixture(t, 2)
	ctx := t.Context()
	p := approved(t, e, s, source)
	// Recreate the legacy table shape in this private database. The owner
	// upgrade must retain old rows, identity, enrollment and qualification.
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE gregale_repair.operations DROP COLUMN payload_purged_at;
ALTER TABLE gregale_repair.plans DROP COLUMN payload_purged_at, DROP COLUMN retained_digest;
DROP TABLE gregale_repair.retention_runs;`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := e.Pool.Exec(ctx, RetentionSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, uuid.NewString(), "post-upgrade"); err != nil {
		t.Fatalf("upgrade invalidated existing approval/enrollment: %v", err)
	}
	if strings.Contains(Schema(), "__RETENTION_") || strings.Contains(RetentionSchema(), "__PURGE_") {
		t.Fatal("unexpanded owner SQL template")
	}
}

func TestRepairRetentionRejectsOldTransactionSnapshots(t *testing.T) {
	e, s, source := fixture(t, 1)
	ctx := t.Context()
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, 1)
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT count(*) FROM gregale_repair.operations`); err != nil {
		t.Fatal(err)
	}
	retentionPurge(t, e, s, batch)
	// A stale snapshot must not reconstruct a new plan from MVCC history after
	// another session has purged that source. The row lock forces a conflict.
	_, checks, err := checkSQL("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readPlan(ctx, tx, `SELECT gregale_repair.create_plan($1::uuid,$2::jsonb,$3)`, source, scopeJSON(s), checks); !errors.Is(err, ErrConflict) {
		t.Fatalf("old transaction resurrected source payload: %v", err)
	}
	_ = tx.Rollback(ctx)
	tx, err = e.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT gregale_repair.apply_retention($1::uuid,$2::jsonb,$3,'operator')`, batch.ID, scopeJSON(s), batch.Digest); !errors.Is(classify(err), ErrInvalid) {
		_ = tx.Rollback(ctx)
		t.Fatalf("direct purge allowed a stale isolation level: %v", err)
	}
	_ = tx.Rollback(ctx)
	// The Engine explicitly uses READ COMMITTED even when a connection's
	// configured default would otherwise retain an obsolete snapshot.
	cfg := e.Pool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	configured := &Engine{Pool: pool}
	if _, err := configured.PurgeRetention(ctx, s, batch.ID, batch.Digest, "operator"); err != nil {
		t.Fatalf("configured default defeated fresh-read purge: %v", err)
	}
}

func TestRepairRetentionMaintenancePrivileges(t *testing.T) {
	e, s, source := fixture(t, 1)
	ctx := t.Context()
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, 1)
	writer := "repair_writer_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	maintainer := "repair_maint_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	writerIdent, maintainerIdent := pgx.Identifier{writer}.Sanitize(), pgx.Identifier{maintainer}.Sanitize()
	for _, name := range []string{writerIdent, maintainerIdent} {
		if _, err := e.Pool.Exec(ctx, "CREATE ROLE "+name+" NOLOGIN"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if _, err := e.Pool.Exec(clean, "DROP OWNED BY "+name+"; DROP ROLE "+name); err != nil {
				t.Error(err)
			}
		})
	}
	_, err := e.Pool.Exec(ctx, "GRANT USAGE ON SCHEMA gregale_repair TO "+writerIdent+", "+maintainerIdent+";"+
		"GRANT SELECT,INSERT,UPDATE ON gregale_repair.operations,gregale_repair.changes TO "+writerIdent+";"+
		"GRANT EXECUTE ON FUNCTION gregale_repair.prepare_retention(jsonb,timestamptz,integer),gregale_repair.get_retention_plan(uuid,jsonb),gregale_repair.apply_retention(uuid,jsonb,text,text) TO "+maintainerIdent)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []string{
		`SELECT gregale_repair.apply_retention('` + batch.ID + `'::uuid,'` + string(scopeJSON(s)) + `'::jsonb,'` + batch.Digest + `','writer')`,
		`UPDATE gregale_repair.operations SET payload_purged_at=clock_timestamp() WHERE id='` + source + `'::uuid`,
	} {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+writerIdent); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('gregale.repair_purge',pg_current_xact_id()::text,true)`); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, attempt); err == nil {
			_ = tx.Rollback(ctx)
			t.Fatal("ordinary writer acquired maintenance authority")
		}
		_ = tx.Rollback(ctx)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+maintainerIdent); err != nil {
		t.Fatal(err)
	}
	var directDelete bool
	if err := tx.QueryRow(ctx, `SELECT has_table_privilege(current_user,'gregale_repair.changes','DELETE')`).Scan(&directDelete); err != nil || directDelete {
		t.Fatal("maintenance role unexpectedly owns raw journal deletion", err)
	}
	if _, err := readRetentionPlan(ctx, tx, `SELECT gregale_repair.apply_retention($1::uuid,$2::jsonb,$3,'maintenance-operator')`, batch.ID, scopeJSON(s), batch.Digest); err != nil {
		t.Fatalf("approved maintenance role cannot purge through the guard: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
