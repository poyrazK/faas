//go:build !no_pg

// adr: 1524
package state_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func recoverySubmissionPG(t *testing.T) (*state.PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	return state.NewPgStore(pool), pool
}

func TestOperatorRecoverySubmissionPostgres(t *testing.T) {
	runRecoverySubmissionTests(t, func(t *testing.T) recoverySubmissionStore { store, _ := recoverySubmissionPG(t); return store })
}

func TestOperatorRecoverySubmissionPostgresAtomicFailures(t *testing.T) {
	for _, fault := range []struct{ name, table, timing, action string }{
		{"intent_insert", "operator_intents", "BEFORE", "RAISE EXCEPTION 'intent unavailable';"},
		{"intent_suppressed", "operator_intents", "BEFORE", "RETURN NULL;"},
		{"audit_insert", "audit_event_outbox", "BEFORE", "RAISE EXCEPTION 'audit unavailable';"},
		{"audit_suppressed", "audit_event_outbox", "BEFORE", "RETURN NULL;"},
		{"receipt_insert", "operator_recovery_submissions", "BEFORE", "RAISE EXCEPTION 'receipt unavailable';"},
		{"receipt_suppressed", "operator_recovery_submissions", "BEFORE", "RETURN NULL;"},
		{"deferred_commit", "operator_recovery_submissions", "AFTER", "RAISE EXCEPTION 'deferred failure';"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			store, pool := recoverySubmissionPG(t)
			f := seedRecoverySubmission(t, store)
			in := f.request(state.OperatorIntentKindForceRestart)
			ddl := "CREATE FUNCTION submission_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN " + fault.action + " END $$; "
			if fault.timing == "AFTER" {
				ddl += "CREATE CONSTRAINT TRIGGER submission_fault AFTER INSERT ON " + fault.table + " DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION submission_fault();"
			} else {
				ddl += "CREATE TRIGGER submission_fault BEFORE INSERT ON " + fault.table + " FOR EACH ROW EXECUTE FUNCTION submission_fault();"
			}
			if _, err := pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SubmitOperatorRecovery(t.Context(), in); err == nil {
				t.Fatal("fault reported acceptance")
			}
			var intents, receipts, audits int
			if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM operator_intents), (SELECT count(*) FROM operator_recovery_submissions), (SELECT count(*) FROM audit_event_outbox)`).Scan(&intents, &receipts, &audits); err != nil {
				t.Fatal(err)
			}
			if intents != 0 || receipts != 0 || audits != 0 {
				t.Fatalf("partial acceptance: intents=%d receipts=%d audits=%d", intents, receipts, audits)
			}
			if _, err := pool.Exec(t.Context(), "DROP TRIGGER submission_fault ON "+fault.table); err != nil {
				t.Fatal(err)
			}
			receipt, err := store.SubmitOperatorRecovery(t.Context(), in)
			if err != nil || receipt.Replayed {
				t.Fatalf("failed request poisoned key: %+v %v", receipt, err)
			}
			// A replacement API/store returns the receipt even after the old cache TTL.
			if _, err := pool.Exec(t.Context(), `UPDATE operator_recovery_submissions SET created_at=now()-interval '10 days'`); err != nil {
				t.Fatal(err)
			}
			replay, err := state.NewPgStore(pool).SubmitOperatorRecovery(t.Context(), in)
			if err != nil || !replay.Replayed || !bytes.Equal(receipt.ResponseBody, replay.ResponseBody) {
				t.Fatalf("replacement/aged replay: %+v %v", replay, err)
			}
			assertOnlyRecoverySubmission(t, f, receipt)
		})
	}
}

func TestOperatorRecoverySubmissionPostgresRetainedEvidence(t *testing.T) {
	store, pool := recoverySubmissionPG(t)
	f := seedRecoverySubmission(t, store)
	in := f.request(state.OperatorIntentKindForcePark)
	receipt, err := store.SubmitOperatorRecovery(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM operator_intents WHERE id=$1`, receipt.IntentID); err == nil {
		t.Fatal("intent deletion erased replay boundary")
	}
	data, err := os.ReadFile("../../migrations/20261017110000001_operator_recovery_submissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if _, err := pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade discarded retained receipts")
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM operator_recovery_submissions`); err != nil {
		t.Fatal(err)
	}
	// Test-only destructive cleanup above makes the reversible empty migration safe.
	for _, migration := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err := pool.Exec(t.Context(), migration); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOperatorRecoverySubmissionPostgresExpiredLegacyReservation(t *testing.T) {
	store, pool := recoverySubmissionPG(t)
	f := seedRecoverySubmission(t, store)
	in := f.request(state.OperatorIntentKindForceRestart)
	if _, err := pool.Exec(t.Context(), `INSERT INTO idempotency_keys (account_id,key,response_status,response_body,created_at)
 VALUES ($1,$2,0,''::bytea,now()-interval '10 days')`, f.actor.ID, "POST /v1/admin/instances/"+f.instance.ID+"/force-restart\n"+in.RequestKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitOperatorRecovery(t.Context(), in); err == nil {
		t.Fatal("expired uncertain reservation was reclaimed")
	}
	var intents int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM operator_intents`).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("legacy reservation executed: %d %v", intents, err)
	}
}
