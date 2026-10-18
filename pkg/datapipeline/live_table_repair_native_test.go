package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func assertNativeLiveTableRepair(t *testing.T, ctx context.Context, store *PostgresStore, engine *Engine, source, target *pgx.Conn, targetURL, reader, account string, tick func() api.DataPipeline, restart func(), p api.DataPipeline) api.DataPipeline {
	get := func() Pipeline {
		t.Helper()
		v, err := store.Get(ctx, account, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	wait := func(condition func(Pipeline) bool) {
		t.Helper()
		var verifiedRows int64
		for until := time.Now().Add(time.Minute); ; {
			p = tick()
			v := get()
			if condition(v) {
				return
			}
			// A 100k-row proof spans bounded ranges and native replication
			// acknowledgements. Bound inactivity, rather than total scan time,
			// so a slower runner can finish while rows keep advancing. The
			// enclosing native test context still bounds the whole journey.
			if progress := v.VerificationProgress; progress != nil && progress.State == "running" && progress.Rows > verifiedRows {
				verifiedRows = progress.Rows
				until = time.Now().Add(time.Minute)
				t.Logf("live repair verification advanced: rows=%d phase=%s", progress.Rows, progress.Phase)
			}
			if p.State == "resync_required" || time.Now().After(until) {
				t.Fatalf("live repair stalled: %+v plan=%+v verification=%+v", p, v.SchemaChange, v.VerificationProgress)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	var heap, orderOID, subOID, pubOID uint32
	var originalTotal string
	if err := target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders'),'public.orders'::regclass::oid,(SELECT oid FROM pg_subscription WHERE subname=$1),(SELECT total::text FROM public.orders WHERE id=1)`, pipelineName(get())).Scan(&heap, &orderOID, &subOID, &originalTotal); err != nil {
		t.Fatal(err)
	}
	if err := source.QueryRow(ctx, `SELECT oid FROM pg_publication WHERE pubname=$1`, pipelineName(get())).Scan(&pubOID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `INSERT INTO public.orders(id,customer,total) SELECT i,'live repair healthy fixture',7 FROM generate_series(900000,998998) i`); err != nil {
		t.Fatal(err)
	}
	var valid bool
	wait(func(v Pipeline) bool {
		return v.State == "live" && target.QueryRow(ctx, `SELECT (SELECT count(*)=100000 FROM public.orders) AND (SELECT count(*)=100000 FROM public.customers)`).Scan(&valid) == nil && valid
	})
	if _, err := target.Exec(ctx, `CREATE FUNCTION gregale_pipeline.reject_live_repair_recopy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'healthy table was recopied'; END $$;
 CREATE TRIGGER reject_live_repair_recopy BEFORE INSERT OR DELETE OR TRUNCATE ON public.orders FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.reject_live_repair_recopy(); ALTER TABLE public.orders ENABLE ALWAYS TRIGGER reject_live_repair_recopy;
 CREATE TABLE gregale_pipeline.live_repair_test_audit(n int);
 CREATE FUNCTION gregale_pipeline.audit_live_repair() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO gregale_pipeline.live_repair_test_audit VALUES(1); RETURN NULL; END $$;
 CREATE TRIGGER audit_live_repair AFTER TRUNCATE ON public.customers FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.audit_live_repair()`); err != nil {
		t.Fatal(err)
	}
	client := startNativeDataAPI(t, ctx, targetURL, reader)
	defer client.close()
	originalReaders := slices.Clone(get().ReadSubjects)
	currentReader := "alice"
	healthyValue := func() int64 {
		t.Helper()
		status, body := client.request("GET", currentReader, "id=eq.1&select=id,total", "")
		var rows []struct{ Total json.Number }
		if status != 200 || json.Unmarshal([]byte(body), &rows) != nil || len(rows) != 1 {
			t.Fatal("healthy reads interrupted", status, body)
		}
		value, err := strconv.ParseInt(strings.SplitN(rows[0].Total.String(), ".", 2)[0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	checkGate := func() {
		t.Helper()
		if status, body := client.requestTable("GET", currentReader, "customers", "id=eq.3&select=id,name", ""); status != 200 || strings.TrimSpace(body) != "[]" {
			t.Fatal("failed live table is readable", status, body)
		}
		_ = healthyValue()
	}
	writeCtx, cancelWrites := context.WithCancel(ctx)
	done := make(chan liveRemovalWriteResult, 1)
	go func() { done <- writeDuringLiveRemoval(writeCtx, source.Config().ConnString()) }()
	stopped := false
	stopWrites := func() {
		if !stopped {
			cancelWrites()
			select {
			case result := <-done:
				if result.err != nil || result.value < 1001 {
					t.Fatal("source writes failed during live repair", result)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("live repair writer did not stop")
			}
			stopped = true
		}
	}
	defer stopWrites()
	if err := ensureLiveRepairReceipt(ctx, target); err != nil {
		t.Fatal(err)
	}
	// The first quarantine transaction fails. Its catalog mismatch must cause
	// another quarantine attempt even after verify_requested is cleared.
	if _, err := target.Exec(ctx, `CREATE SEQUENCE gregale_pipeline.fail_live_quarantine_once;
 CREATE FUNCTION gregale_pipeline.fail_live_quarantine() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF nextval('gregale_pipeline.fail_live_quarantine_once')=1 THEN RAISE EXCEPTION 'injected quarantine rollback'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_live_quarantine BEFORE INSERT ON gregale_pipeline.live_repairs FOR EACH ROW EXECUTE FUNCTION gregale_pipeline.fail_live_quarantine()`); err != nil {
		t.Fatal(err)
	}
	manual, err := store.Control(ctx, account, p.ID, get().Revision, "verify")
	if err != nil {
		t.Fatal(err)
	}
	p = manual.DataPipeline
	wait(func(v Pipeline) bool {
		return v.VerificationProgress != nil && v.VerificationProgress.State == "running" && v.VerificationProgress.Rows >= 8000
	})
	failedRun := get().VerificationProgress.ID
	corruption, err := target.Exec(ctx, `UPDATE public.customers SET name='corrupt live imported row' WHERE id=3`)
	if err != nil || corruption.RowsAffected() != 1 {
		t.Fatal("corruption did not affect exactly one existing live row", err)
	}
	originalTarget := engine.Target
	defer func() { engine.Target = originalTarget }()
	lostQuarantine := false
	engine.Target = func(c context.Context, a, id string, run func(*pgx.Conn, string) error) error {
		err := originalTarget(c, a, id, run)
		var exists bool
		if err == nil && !lostQuarantine {
			// Inspect committed state independently of the worker's operation
			// deadline. Cancelling that deadline must not close the shared
			// fixture connection and disguise later results as provider loss.
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var present bool
			if err = target.QueryRow(probe, `SELECT to_regclass('gregale_pipeline.live_repairs') IS NOT NULL`).Scan(&present); err != nil {
				t.Fatalf("inspect quarantine receipt table: %v", err)
			}
			if present {
				err = target.QueryRow(probe, `SELECT EXISTS(SELECT 1 FROM gregale_pipeline.live_repairs WHERE pipeline_id=$1 AND stage='quarantined')`, p.ID).Scan(&exists)
				if err != nil {
					t.Fatalf("inspect committed quarantine receipt: %v", err)
				}
			}
			if err == nil && exists {
				lostQuarantine = true
				return ErrUnavailable
			}
		}
		return err
	}
	wait(func(Pipeline) bool { return lostQuarantine })
	checkGate()
	if err = target.QueryRow(ctx, `SELECT last_value>=2 FROM gregale_pipeline.fail_live_quarantine_once`).Scan(&valid); err != nil || !valid {
		t.Fatal("failed quarantine transaction was not retried", err)
	}
	if _, err = target.Exec(ctx, `DROP TRIGGER fail_live_quarantine ON gregale_pipeline.live_repairs`); err != nil {
		t.Fatal(err)
	}
	engine.Target = originalTarget
	restart()
	wait(func(v Pipeline) bool { return v.Reason == "live_table_mismatch" })
	detected := get()
	if !slices.Equal(RecoverableTables(detected), []string{"customers"}) || detected.SchemaChange.SelectionID != failedRun || detected.Generation != p.Generation {
		t.Fatal("live mismatch lost recoverable scope")
	}
	if err = store.TableRecovery(ctx, account, p.ID, detected.Revision, "", "orders"); !errors.Is(err, ErrConflict) {
		t.Fatal("healthy table selected for repair", err)
	}
	if err = store.TableRecovery(ctx, account, p.ID, detected.Revision, "", "customers"); err != nil {
		t.Fatal(err)
	}
	wait(func(v Pipeline) bool { return v.Reason == "table_recovery_pending" })
	pending := get()
	if err = target.QueryRow(ctx, `SELECT name='corrupt live imported row' FROM public.customers WHERE id=3`).Scan(&valid); err != nil || !valid {
		t.Fatal("preview changed imported data", err)
	}
	if err = store.TableRecovery(ctx, account, p.ID, pending.Revision, strings.Repeat("0", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("different repair approved", err)
	}
	if err = store.TableRecovery(ctx, account, p.ID, pending.Revision, pending.SchemaChange.Recovery.ID); err != nil {
		t.Fatal(err)
	}
	approved := get()
	publisher, err := pgxpool.New(ctx, source.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	always := func(context.Context) error { return nil }
	wait(func(v Pipeline) bool { return v.SchemaChange.Recovery.Stage == "retiring" })
	checkGate()
	// A customer transaction can outlast the short source lock budget. Prove
	// that both the direct operation and the worker leave retirement replayable
	// without changing publication membership or exposing the quarantined table.
	func() {
		held, err := source.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = held.Rollback(ctx) }()
		if _, err = held.Exec(ctx, `LOCK TABLE public.customers IN ROW EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		checked := false
		err = recoveryPublication(ctx, approved, publisher, false, func(context.Context) error {
			checked = true
			return nil
		})
		var lockErr *pgconn.PgError
		if !errors.As(err, &lockErr) || lockErr.Code != "55P03" || checked {
			t.Fatal("source lock contention did not abort before publication commit", err, checked)
		}
		p = tick()
		v := get()
		if p.State != "blocked" || p.Reason != "database_unavailable" || v.SchemaChange.Recovery.Stage != "retiring" {
			t.Fatal("source lock contention lost the durable retirement stage", p, v.SchemaChange)
		}
		if err = ensurePublication(ctx, approved, publisher); err != nil {
			t.Fatal("source lock contention changed publication membership", err)
		}
		checkGate()
		if err = held.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}()
	// These direct calls inject lost acknowledgements between worker ticks.
	// Like the worker, retry only transient source lock contention while mixed
	// table writes continue. Keep both the SQL lock budget and journey deadline.
	publish := func(include bool) {
		t.Helper()
		attempt, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		for {
			err := recoveryPublication(attempt, approved, publisher, include, always)
			if err == nil {
				return
			}
			var lockErr *pgconn.PgError
			if !errors.As(err, &lockErr) || (lockErr.Code != "55P03" && lockErr.Code != "40P01") || attempt.Err() != nil {
				t.Fatalf("publication change failed (include=%t): %v", include, err)
			}
			t.Logf("retrying publication lock contention (include=%t): %s", include, lockErr.Code)
			select {
			case <-attempt.Done():
				t.Fatal("publication lock contention did not resolve", attempt.Err())
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	publish(false)
	checks := 0
	err = retireRecoveredRelation(ctx, approved, publisher, target, func(context.Context) error {
		checks++
		if checks == 2 {
			return ErrUnavailable
		}
		return nil
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("native retirement acknowledgement not lost", err)
	}
	restart()
	wait(func(v Pipeline) bool { return v.SchemaChange.Recovery.Stage == "detached" })
	if _, err = source.Exec(ctx, `ALTER TABLE public.customers ADD COLUMN repair_drift text`); err != nil {
		t.Fatal(err)
	}
	if err = clearRecoveredTable(ctx, approved, publisher, target, always); !isBlockedReason(err, "table_addition_source_changed") {
		t.Fatal("changed source allowed clearing", err)
	}
	if _, err = source.Exec(ctx, `ALTER TABLE public.customers DROP COLUMN repair_drift`); err != nil {
		t.Fatal(err)
	}
	if err = clearRecoveredTable(ctx, approved, publisher, target, func(context.Context) error { return ErrConflict }); !errors.Is(err, ErrConflict) {
		t.Fatal("unfenced clear accepted", err)
	}
	if err = target.QueryRow(ctx, `SELECT name='corrupt live imported row' FROM public.customers WHERE id=3`).Scan(&valid); err != nil || !valid {
		t.Fatal("unapproved/unfenced clear committed", err)
	}
	wait(func(v Pipeline) bool { return v.SchemaChange.Recovery.Stage == "cleared" })
	restart()
	publish(true)
	wait(func(v Pipeline) bool { return v.SchemaChange.Recovery.Stage == "published" })
	locker, err := pgx.Connect(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Close(context.WithoutCancel(ctx)) }()
	locked, err := locker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locked.Rollback(context.WithoutCancel(ctx)) }()
	// Block COPY inside its INSERT trigger. A SHARE relation lock would also
	// block the apply worker opening an unready relation in a mixed-table source
	// transaction, preventing it from reaching the normal skip/copy-worker logic.
	if _, err = target.Exec(ctx, `CREATE FUNCTION gregale_pipeline.hold_live_repair_copy() RETURNS trigger LANGUAGE plpgsql AS `+literal(`BEGIN PERFORM pg_advisory_xact_lock(hashtextextended(`+literal(p.ID)+`,51951)); RETURN NEW; END`)+`;
 CREATE TRIGGER hold_live_repair_copy BEFORE INSERT ON public.customers FOR EACH ROW EXECUTE FUNCTION gregale_pipeline.hold_live_repair_copy(); ALTER TABLE public.customers ENABLE ALWAYS TRIGGER hold_live_repair_copy`); err != nil {
		t.Fatal(err)
	}
	if _, err = locked.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,51951))`, p.ID); err != nil {
		t.Fatal(err)
	}
	checks = 0
	err = copyRecoveredRelation(ctx, approved, publisher, target, func(context.Context) error {
		checks++
		if checks == 2 {
			return ErrUnavailable
		}
		return nil
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("native copy acknowledgement not lost", err)
	}
	restart()
	wait(func(v Pipeline) bool { return v.SchemaChange.Recovery.Stage == "copying" })
	wait(func(v Pipeline) bool {
		return len(v.SchemaChange.Copies) == 1 && v.SchemaChange.Copies[0].WorkerActive && v.SchemaChange.Copies[0].NativeState == "d"
	})
	// Revoke Alice and grant Bob during a real blocked COPY and mixed-table
	// source writes. Preview must leave access unchanged.
	beforeReaders := get()
	if err = store.Readers(ctx, account, p.ID, beforeReaders.Revision, "preview", "", []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	_ = healthyValue()
	previewReaders := get()
	if err = store.Readers(ctx, account, p.ID, previewReaders.Revision, "approve", previewReaders.ReaderChange.ID, nil); err != nil {
		t.Fatal(err)
	}
	leasedReaders, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	appliedReaders, err := applyReaders(ctx, leasedReaders, target, func(context.Context) error {
		v := get()
		if v.Revision != leasedReaders.Revision || v.LeaseToken != leasedReaders.LeaseToken || !v.LeaseUntil.After(time.Now()) {
			return ErrConflict
		}
		return nil
	})
	if err != nil || appliedReaders.State != "applied" {
		t.Fatal("reader change failed behind COPY", err)
	}
	// Drop the catalog acknowledgement, then reconstruct the worker.
	if get().ReaderChange.State != "approved" {
		t.Fatal("lost reader acknowledgement was not exercised")
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	currentReader = "bob"
	for _, table := range []string{"customers", "orders"} {
		if status, body := client.requestTable("GET", "alice", table, "select=id&limit=1", ""); status != 200 || strings.TrimSpace(body) != "[]" {
			t.Fatal("revoked reader retained access", table, status, body)
		}
	}
	checkGate()
	restart()
	wait(func(v Pipeline) bool { return v.ReaderChange != nil && v.ReaderChange.State == "applied" })
	if !slices.Equal(get().ReadSubjects, []string{"bob"}) {
		t.Fatal("restart lost applied reader set")
	}
	previous := healthyValue()
	for until := time.Now().Add(10 * time.Second); healthyValue() <= previous; {
		p = tick()
		if time.Now().After(until) {
			rows, err := target.Query(ctx, `SELECT pid,backend_type,coalesce(wait_event_type,''),coalesce(wait_event,''),pg_blocking_pids(pid)::text FROM pg_stat_activity WHERE datname=current_database()`)
			if err != nil {
				t.Fatal(err)
			}
			activity, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
				var pid int32
				var kind, waitType, waitEvent, blocked string
				err := row.Scan(&pid, &kind, &waitType, &waitEvent, &blocked)
				return strconv.Itoa(int(pid)) + " " + kind + " " + waitType + " " + waitEvent + " " + blocked, err
			})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				stopped = true
				t.Fatal("source writer stopped before CDC check", result)
			default:
			}
			t.Fatal("healthy CDC stalled behind blocked repair COPY", activity)
		}
		time.Sleep(20 * time.Millisecond)
	}
	checkGate()
	originalRefresh := engine.RefreshSchema
	defer func() { engine.RefreshSchema = originalRefresh }()
	refreshReady := false
	engine.RefreshSchema = func(_ context.Context, v Pipeline, change SchemaChange) ([]SchemaRefresh, bool, error) {
		if !liveTableRepair(v.SchemaChange) || change.ID != approved.SchemaApproval || change.Retry != 1 {
			t.Fatal("repair reused another API refresh")
		}
		return nil, refreshReady, nil
	}
	if err = locked.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wait(func(v Pipeline) bool {
		return v.VerificationProgress != nil && v.VerificationProgress.State == "running" && v.VerificationProgress.Rows >= 8000
	})
	if v := get().VerificationProgress; v.ID == failedRun || !slices.Equal(v.Tables, []string{"customers"}) {
		t.Fatal("repair reused failed proof or verified healthy tables", v)
	}
	restart()
	wait(func(v Pipeline) bool { return v.Reason == "schema_api_refresh_pending" })
	checkGate()
	stopWrites()
	refreshReady = true
	// Complete the private result but deliberately omit catalog Finish.
	old := get()
	for until := time.Now().Add(20 * time.Second); ; {
		ob := Observation{State: "blocked"}
		handled, err := engine.reconcileLiveRepair(ctx, old, source.Config().ConnString(), publisher, target, ownerForNativeTable(t, ctx, target), &ob, always)
		if err != nil || !handled {
			t.Fatal("private repair completion failed", handled, err)
		}
		if err = target.QueryRow(ctx, `SELECT stage='complete' FROM gregale_pipeline.live_repairs WHERE pipeline_id=$1`, p.ID).Scan(&valid); err != nil {
			t.Fatal(err)
		}
		if valid {
			break
		}
		if time.Now().After(until) {
			t.Fatal("private repair never sealed", ob)
		}
	}
	restart()
	wait(func(v Pipeline) bool {
		return v.State == "live" && v.SchemaChange.State == "complete" && v.SchemaChange.Recovery.State == "complete"
	})
	if status, body := client.requestTable("GET", "bob", "customers", "id=eq.3&select=id,name", ""); status != 200 || strings.TrimSpace(body) == "[]" || strings.Contains(body, "corrupt live imported row") {
		t.Fatal("repaired table did not reopen correctly", status, body)
	}
	if status, body := client.requestTable("GET", "alice", "customers", "select=id", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatal("repair bypassed reader restrictions", status, body)
	}
	if err = target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid=$1 AND pg_relation_filenode('public.orders')=$2
 AND (SELECT oid=$3 AND subenabled FROM pg_subscription WHERE subname=$4)
 AND (SELECT readable AND schema_phase='' FROM gregale_pipeline.mirrors WHERE pipeline_id=$5)
 AND (SELECT count(*)=100000 FROM public.customers) AND (SELECT count(*)=1 FROM gregale_pipeline.live_repair_test_audit)`, orderOID, heap, subOID, pipelineName(get()), p.ID).Scan(&valid); err != nil || !valid {
		t.Fatal("healthy storage/shared stream changed or repair cleared twice", err)
	}
	if err = source.QueryRow(ctx, `SELECT (SELECT oid=$1 FROM pg_publication WHERE pubname=$2)
 AND (SELECT count(*)=1 AND bool_and(slot_name=$2) FROM pg_replication_slots WHERE database=current_database())`, pubOID, pipelineName(get())).Scan(&valid); err != nil || !valid {
		t.Fatal("publication/main slot changed or copy slot leaked", err)
	}
	if get().Generation != p.Generation || !slices.Equal(get().Tables, []string{"customers", "orders"}) {
		t.Fatal("repair reset pipeline generation/selection")
	}
	// Restore the fixture's original reader list for subsequent workflows.
	v := get()
	if err = store.Readers(ctx, account, p.ID, v.Revision, "preview", "", originalReaders); err != nil {
		t.Fatal(err)
	}
	v = get()
	if err = store.Readers(ctx, account, p.ID, v.Revision, "approve", v.ReaderChange.ID, nil); err != nil {
		t.Fatal(err)
	}
	wait(func(v Pipeline) bool { return v.ReaderChange.State == "applied" })
	currentReader = "alice"
	_ = healthyValue()
	if _, err = target.Exec(ctx, `DROP TRIGGER reject_live_repair_recopy ON public.orders; DROP TRIGGER audit_live_repair ON public.customers; DROP TRIGGER hold_live_repair_copy ON public.customers`); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, `DELETE FROM public.orders WHERE id BETWEEN 900000 AND 998998`); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, `UPDATE public.orders SET total=$1 WHERE id=1`, originalTotal); err != nil {
		t.Fatal(err)
	}
	wait(func(Pipeline) bool {
		return target.QueryRow(ctx, `SELECT count(*)=1001 AND bool_or(id=1 AND total::text=$1) FROM public.orders`, originalTotal).Scan(&valid) == nil && valid
	})
	return p
}
