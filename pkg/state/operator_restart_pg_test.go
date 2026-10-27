//go:build !no_pg

package state_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func restartPGStore(t *testing.T) (*state.PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), pool
}

func TestOperatorRestartPostgres(t *testing.T) {
	runRestartStoreTests(t, func(t *testing.T) restartTestStore { store, _ := restartPGStore(t); return store })
}

func TestOperatorRestartPostgresAtomicFailures(t *testing.T) {
	for _, fault := range []struct{ name, ddl, cleanup string }{
		{"audit_insert", `CREATE FUNCTION restart_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$;
 CREATE TRIGGER restart_fault BEFORE INSERT ON audit_event_outbox FOR EACH ROW EXECUTE FUNCTION restart_fault();`, `DROP TRIGGER restart_fault ON audit_event_outbox`},
		{"instance_write", `CREATE FUNCTION restart_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'state unavailable'; END $$;
 CREATE TRIGGER restart_fault BEFORE UPDATE ON instances FOR EACH ROW EXECUTE FUNCTION restart_fault();`, `DROP TRIGGER restart_fault ON instances`},
		{"suppressed_instance_write", `CREATE FUNCTION restart_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
 CREATE TRIGGER restart_fault BEFORE UPDATE ON instances FOR EACH ROW EXECUTE FUNCTION restart_fault();`, `DROP TRIGGER restart_fault ON instances`},
		{"intent_write", `CREATE FUNCTION restart_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'intent unavailable'; END $$;
 CREATE TRIGGER restart_fault BEFORE UPDATE ON operator_intents FOR EACH ROW EXECUTE FUNCTION restart_fault();`, `DROP TRIGGER restart_fault ON operator_intents`},
		{"commit", `CREATE FUNCTION restart_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'deferred failure'; END $$;
 CREATE CONSTRAINT TRIGGER restart_fault AFTER UPDATE ON operator_intents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION restart_fault();`, `DROP TRIGGER restart_fault ON operator_intents`},
	} {
		t.Run(fault.name, func(t *testing.T) {
			store, pool := restartPGStore(t)
			f := seedRestart(t, store)
			privateLease := uuid.NewString()
			if _, err := pool.Exec(t.Context(), `UPDATE instances SET lease_token=$2, migrated_at=clock_timestamp() WHERE id=$1`, f.target.InstanceID, privateLease); err != nil {
				t.Fatal(err)
			}
			planned := f.prepare(t)
			if planned.Target.MigrationLeaseToken != privateLease || planned.Target.MigratedAt == nil {
				t.Fatal("original migration lineage was not captured")
			}
			f.acknowledge(t)
			if _, err := pool.Exec(t.Context(), fault.ddl); err != nil {
				t.Fatal(err)
			}
			completion := state.OperatorRestartCompletion{IntentID: f.intent.ID, ClaimToken: f.intent.ClaimToken, Status: state.OperatorIntentSucceeded}
			if _, err := store.CompleteOperatorRestart(t.Context(), completion); err == nil {
				t.Fatal("fault reported success")
			}
			row, err := store.GetOperatorIntent(t.Context(), f.intent.ID)
			if err != nil || row.Status != state.OperatorIntentRunning || row.FinishedAt != nil || row.RestartExecution.Phase != state.RestartDestroyed {
				t.Fatalf("partial outcome committed: %+v, %v", row, err)
			}
			instance, err := store.InstanceByID(t.Context(), f.target.InstanceID)
			if err != nil || instance.State != string(state.StateRunning) {
				t.Fatalf("partial state committed: %+v, %v", instance, err)
			}
			ports, err := store.ListHostPortLeases(t.Context(), f.target.NodeID, f.target.InstanceID)
			if err != nil || len(ports) != 1 {
				t.Fatalf("failed transaction released ports: %v, %v", ports, err)
			}
			var audits int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_event_outbox`).Scan(&audits); err != nil || audits != 0 {
				t.Fatalf("partial audit committed: %d, %v", audits, err)
			}
			if _, err := pool.Exec(t.Context(), fault.cleanup); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CompleteOperatorRestart(t.Context(), completion); err != nil {
				t.Fatal(err)
			}
			var terminalStamped bool
			if err := pool.QueryRow(t.Context(), `SELECT terminal_at IS NOT NULL FROM instances WHERE id=$1`, f.target.InstanceID).Scan(&terminalStamped); err != nil || !terminalStamped {
				t.Fatalf("terminal retention timestamp missing: %v", err)
			}
		})
	}
}

func TestOperatorRestartPostgresChangedWakeAndReclaim(t *testing.T) {
	for _, changed := range []struct{ name, query string }{
		{"wake", "UPDATE instances SET wake_id=gen_random_uuid() WHERE id=$1"},
		{"migration_history", "UPDATE instances SET migrated_at=clock_timestamp() WHERE id=$1"},
		{"migration_lease", "UPDATE instances SET lease_token=gen_random_uuid()::text WHERE id=$1"},
	} {
		t.Run(changed.name, func(t *testing.T) {
			store, pool := restartPGStore(t)
			f := seedRestart(t, store)
			privateLease := uuid.NewString()
			if _, err := pool.Exec(t.Context(), `UPDATE instances SET lease_token=$2, migrated_at=clock_timestamp() WHERE id=$1`, f.target.InstanceID, privateLease); err != nil {
				t.Fatal(err)
			}
			planned := f.prepare(t)
			if planned.Target.MigrationLeaseToken != privateLease || planned.Target.MigratedAt == nil {
				t.Fatal("original migration lineage was not captured")
			}
			f.acknowledge(t)
			if _, err := pool.Exec(t.Context(), changed.query, f.target.InstanceID); err != nil {
				t.Fatal(err)
			}
			_, err := store.CompleteOperatorRestart(t.Context(), state.OperatorRestartCompletion{IntentID: f.intent.ID, ClaimToken: f.intent.ClaimToken, Status: state.OperatorIntentSucceeded})
			if !errors.Is(err, state.ErrOperatorRestartTargetChanged) {
				t.Fatalf("changed boot accepted: %v", err)
			}
			outcome, err := store.CompleteOperatorRestart(t.Context(), state.OperatorRestartCompletion{IntentID: f.intent.ID, ClaimToken: f.intent.ClaimToken, Status: state.OperatorIntentOutcomeUnknown, Error: "target changed after acknowledged teardown"})
			if err != nil {
				t.Fatal(err)
			}
			audit, err := store.ClaimAuditEvent(t.Context(), "restart-redaction-test", time.Minute)
			if err != nil || audit.ID != outcome.AuditOutboxID || strings.Contains(string(audit.Data), privateLease) || strings.Contains(string(audit.Data), "migration_lease_token") {
				t.Fatalf("private migration credential in audit: id=%d error=%v", audit.ID, err)
			}
			if _, err := store.ClaimPendingOperatorIntent(t.Context()); !errors.Is(err, state.ErrOperatorIntentNotFound) {
				t.Fatalf("changed target entered a reclaim loop: %v", err)
			}
			if _, err := store.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			i, err := store.InstanceByID(t.Context(), f.target.InstanceID)
			if err != nil || i.State != string(state.StateRunning) {
				t.Fatalf("replacement boot changed: %+v, %v", i, err)
			}
		})
	}
}

func TestOperatorRestartMigrationQuarantinesLegacyRunning(t *testing.T) {
	pool := pgtest.Open(t)
	data, err := os.ReadFile("../../migrations/20261017090000001_operator_restart_execution.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	_, err = pool.Exec(t.Context(), `CREATE TABLE operator_intents (id uuid PRIMARY KEY, kind text NOT NULL, status text NOT NULL, requested_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz, error text, CONSTRAINT operator_intents_status_check CHECK (status IN ('pending','running','succeeded','failed','cancelled')));
 INSERT INTO operator_intents (id,kind,status,finished_at,error) VALUES ('11111111-1111-1111-1111-111111111111','force_restart','running',NULL,NULL),
 ('22222222-2222-2222-2222-222222222222','force_restart','pending',NULL,NULL),
 ('33333333-3333-3333-3333-333333333333','force_park','running',NULL,NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	var status, message string
	if err := pool.QueryRow(t.Context(), `SELECT status,error FROM operator_intents WHERE id='11111111-1111-1111-1111-111111111111'`).Scan(&status, &message); err != nil {
		t.Fatal(err)
	}
	if status != "outcome_unknown" || !strings.Contains(message, "reconciliation") {
		t.Fatalf("legacy action replayable: %s %s", status, message)
	}
	if _, err := pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade discarded unresolved execution evidence")
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM operator_intents WHERE status='outcome_unknown'`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err := pool.Exec(t.Context(), migration); err != nil {
			t.Fatal(err)
		}
	}
	var pending, parked int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE kind='force_restart' AND status='pending'), count(*) FILTER (WHERE kind='force_park' AND status='running') FROM operator_intents`).Scan(&pending, &parked); err != nil || pending != 1 || parked != 1 {
		t.Fatalf("unrelated legacy states changed: %d %d %v", pending, parked, err)
	}
}
