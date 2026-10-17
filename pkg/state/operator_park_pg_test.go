//go:build !no_pg

package state_test

// adr: 1525

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func parkPGStore(t *testing.T) (*state.PgStore, *pgxpool.Pool) {
	t.Helper()
	p := pgtest.OpenMigrated(t)
	return state.NewPgStore(p), p
}
func TestOperatorParkPostgres(t *testing.T) {
	runParkStoreTests(t, func(t *testing.T) parkTestStore { s, _ := parkPGStore(t); return s })
}

func TestOperatorParkPostgresAtomicCompletion(t *testing.T) {
	for _, fault := range []struct{ name, ddl, cleanup string }{
		{"state", `CREATE FUNCTION park_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'state unavailable'; END $$; CREATE TRIGGER park_fault BEFORE UPDATE ON instances FOR EACH ROW EXECUTE FUNCTION park_fault();`, `DROP TRIGGER park_fault ON instances`},
		{"suppressed_state", `CREATE FUNCTION park_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$; CREATE TRIGGER park_fault BEFORE UPDATE ON instances FOR EACH ROW EXECUTE FUNCTION park_fault();`, `DROP TRIGGER park_fault ON instances`},
		{"suppressed_journal", `CREATE FUNCTION park_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$; CREATE TRIGGER park_fault BEFORE UPDATE ON operator_park_executions FOR EACH ROW EXECUTE FUNCTION park_fault();`, `DROP TRIGGER park_fault ON operator_park_executions`},
		{"audit", `CREATE FUNCTION park_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$; CREATE TRIGGER park_fault BEFORE INSERT ON audit_event_outbox FOR EACH ROW EXECUTE FUNCTION park_fault();`, `DROP TRIGGER park_fault ON audit_event_outbox`},
		{"snapshot_handoff", `CREATE FUNCTION park_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'handoff unavailable'; END $$; CREATE TRIGGER park_fault BEFORE INSERT ON notification_outbox FOR EACH ROW EXECUTE FUNCTION park_fault();`, `DROP TRIGGER park_fault ON notification_outbox`},
		{"intent", `CREATE FUNCTION park_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'intent unavailable'; END $$; CREATE TRIGGER park_fault BEFORE UPDATE ON operator_intents FOR EACH ROW EXECUTE FUNCTION park_fault();`, `DROP TRIGGER park_fault ON operator_intents`},
		{"commit", `CREATE FUNCTION park_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'commit unavailable'; END $$; CREATE CONSTRAINT TRIGGER park_fault AFTER UPDATE ON operator_intents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION park_fault();`, `DROP TRIGGER park_fault ON operator_intents`},
	} {
		t.Run(fault.name, func(t *testing.T) {
			s, p := parkPGStore(t)
			f := seedPark(t, s)
			f.prepare(t)
			e := f.start(t)
			if err := s.AcknowledgeOperatorPark(t.Context(), f.intent.ID, e.EffectToken, capturedParkReceipt()); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Exec(t.Context(), fault.ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, "")); err == nil {
				t.Fatal("fault returned success")
			}
			row, err := s.GetOperatorIntent(t.Context(), f.intent.ID)
			if err != nil || row.Status != state.OperatorIntentRunning || row.ParkExecution.Phase != state.ParkAcknowledged {
				t.Fatalf("partial outcome=%+v %v", row, err)
			}
			var instanceState string
			var blocked bool
			if err := p.QueryRow(t.Context(), `SELECT state,exclusive_capture_blocked FROM instances WHERE id=$1`, f.target.InstanceID).Scan(&instanceState, &blocked); err != nil || instanceState != "snapshotting" || !blocked {
				t.Fatalf("partial state=%s blocked=%v %v", instanceState, blocked, err)
			}
			ports, err := s.ListHostPortLeases(t.Context(), f.target.NodeID, f.target.InstanceID)
			if err != nil || len(ports) != 1 {
				t.Fatalf("partial ports=%+v %v", ports, err)
			}
			var count int
			if err := p.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM audit_event_outbox)+(SELECT count(*) FROM notification_outbox WHERE channel='snapshot_written')`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial handoffs=%d %v", count, err)
			}
			if _, err := p.Exec(t.Context(), fault.cleanup); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, "")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, "")); err != nil {
				t.Fatal(err)
			}
			if err := p.QueryRow(t.Context(), `SELECT count(*) FROM notification_outbox WHERE channel='snapshot_written'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("snapshot handoffs=%d %v", count, err)
			}
		})
	}
}

func TestOperatorParkPostgresPreparationRollbackAndLineage(t *testing.T) {
	t.Run("grant_failure_rolls_back_state_and_barrier", func(t *testing.T) {
		s, p := parkPGStore(t)
		f := seedPark(t, s)
		f.target.Measured = true
		if _, err := s.PrepareOperatorPark(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target); err == nil {
			t.Fatal("missing measured parent accepted")
		}
		var status string
		var blocked bool
		if err := p.QueryRow(t.Context(), `SELECT state,exclusive_capture_blocked FROM instances WHERE id=$1`, f.target.InstanceID).Scan(&status, &blocked); err != nil || status != "running" || blocked {
			t.Fatalf("partial prepare=%s %v %v", status, blocked, err)
		}
	})
	t.Run("late_receipt_cannot_stop_new_wake", func(t *testing.T) {
		s, p := parkPGStore(t)
		f := seedPark(t, s)
		f.prepare(t)
		e := f.start(t)
		if err := s.AcknowledgeOperatorPark(t.Context(), f.intent.ID, e.EffectToken, capturedParkReceipt()); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Exec(t.Context(), `UPDATE instances SET wake_id=gen_random_uuid(),state='running' WHERE id=$1`, f.target.InstanceID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, "")); err == nil {
			t.Fatal("replacement completed")
		}
		if _, err := s.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentOutcomeUnknown, "replacement requires review")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ClaimPendingOperatorIntent(t.Context()); err == nil {
			t.Fatal("replacement automatically claimed")
		}
	})
}

func TestOperatorParkMigrationQuarantinesLegacy(t *testing.T) {
	s, p := parkPGStore(t)
	f := seedPark(t, s)
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../migrations/20261017120000001_operator_park_execution.sql"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	// Recreate the actual pre-upgrade schema. Replaying an already installed
	// migration must preserve requests accepted by the durable executor.
	if _, err := p.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetOperatorIntent(t.Context(), f.intent.ID)
	if err != nil || row.Status != state.OperatorIntentOutcomeUnknown || row.ParkExecution != nil {
		t.Fatalf("legacy=%+v %v", row, err)
	}
	if _, err := s.ClaimPendingOperatorIntent(t.Context()); err == nil {
		t.Fatal("legacy execution redispatched")
	}
}

func TestOperatorParkPostgresSuppressedBarrierRollsBackPreparation(t *testing.T) {
	s, p := parkPGStore(t)
	f := seedPark(t, s)
	_, err := p.Exec(t.Context(), `CREATE FUNCTION park_barrier_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
 CREATE TRIGGER park_barrier_fault BEFORE UPDATE ON instances FOR EACH ROW
 WHEN (NEW.exclusive_capture_blocked AND NOT OLD.exclusive_capture_blocked) EXECUTE FUNCTION park_barrier_fault();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareOperatorPark(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target); err == nil {
		t.Fatal("suppressed barrier accepted")
	}
	var instanceState string
	var blocked bool
	if err := p.QueryRow(t.Context(), `SELECT state,exclusive_capture_blocked FROM instances WHERE id=$1`, f.target.InstanceID).Scan(&instanceState, &blocked); err != nil || instanceState != "running" || blocked {
		t.Fatalf("partial prepare=%s %v %v", instanceState, blocked, err)
	}
}

func TestOperatorParkPostgresDowngradePreservesEvidence(t *testing.T) {
	s, p := parkPGStore(t)
	f := seedPark(t, s)
	f.prepare(t)
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../migrations/20261017120000001_operator_park_execution.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down := strings.Split(string(raw), "-- +goose Down")[1]
	down = strings.ReplaceAll(strings.ReplaceAll(down, "-- +goose StatementBegin", ""), "-- +goose StatementEnd", "")
	if _, err := p.Exec(t.Context(), down); err == nil {
		t.Fatal("downgrade discarded journal")
	}
	if _, err := s.GetOperatorPark(t.Context(), f.intent.ID); err != nil {
		t.Fatal("downgrade removed evidence", err)
	}
}
