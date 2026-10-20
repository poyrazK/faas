//go:build !no_pg

package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgInstanceApplicationStandardNativeBoot(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	state.StandardNativeBootLifecycle(t, s)
}

func TestPgInstanceApplicationStandardNativeReceiptPublicationRollback(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	f := state.NewRuntimeCaptureFixture(t, s, true)
	ins, _, receipt := state.NativeBootTestAttempt(t, s, f, state.StateColdBooting)
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION refuse_native_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='running' THEN RAISE EXCEPTION 'injected publication refusal'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER zz_refuse_native_publication BEFORE UPDATE OF state ON instances FOR EACH ROW EXECUTE FUNCTION refuse_native_publication();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateRunning, receipt); err == nil {
		t.Fatal("injected publication failure was ignored")
	}
	state.AssertNativeBootUnpublished(t, s, ins)
	var received bool
	if err := pool.QueryRow(t.Context(), `SELECT receipt IS NOT NULL FROM instance_application_standard_boots WHERE instance_id=$1`, ins.ID).Scan(&received); err != nil || received {
		t.Fatalf("failed publication committed its receipt: %v %v", received, err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER zz_refuse_native_publication ON instances`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateRunning, receipt); err != nil {
		t.Fatalf("rollback could not retry saved grant: %v", err)
	}
}

func TestPgInstanceApplicationStandardNativeExpiredSavedGrant(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	f := state.NewRuntimeCaptureFixture(t, s, true)
	ins, err := s.CreateInstance(t.Context(), (*f.App()).ID, (*f.Dep()).ID, string(state.StateColdBooting), 128, (*f.NodeID()), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	binding := state.RuntimeCaptureTestBinding(t, s, ins)
	binding.ExpiresAtUnixNano = time.Now().Add(150 * time.Millisecond).UnixNano()
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	// Internal SQL writer simulates a shorter bounded grant. The public store
	// always owns its issuance clock; no database history is modified here.
	if _, err := pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, binding.Token, ins.ID, ins.State, raw); err != nil {
		t.Fatal(err)
	}
	receipt := runtimeadmission.Receipt{Binding: binding, NativeInputHash: strings.Repeat("b", 64), Netns: "expired-native", HostIP: "10.100.0.8", LeaseUID: 20008, CompletedAtUnixNano: time.Now().UnixNano()}
	time.Sleep(time.Until(time.Unix(0, binding.ExpiresAtUnixNano)) + time.Millisecond)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateRunning, receipt); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired durable grant published: %v", err)
	}
	raw, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `UPDATE instance_application_standard_boots SET receipt=$2::jsonb,received_at=clock_timestamp() WHERE token=$1`, binding.Token, raw)
	if !errors.Is(state.MapErr(err), state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw receipt writer bypassed exclusive expiry: %v", err)
	}
	state.AssertNativeBootUnpublished(t, s, ins)
}
func TestPgInstanceApplicationStandardNativeRestart(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	state.StandardNativeBootRestart(t, s)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM instance_application_standard_boots WHERE receipt IS NOT NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale receipt persisted: %d %v", count, err)
	}
}
func TestPgInstanceApplicationStandardNativeStolenState(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	state.StandardNativeBootStolenState(t, s)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM instance_application_standard_boots WHERE receipt IS NOT NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stolen state persisted receipt: %d %v", count, err)
	}
}

func TestPgInstanceApplicationStandardNativeRawGuardsAndContention(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	f := state.NewRuntimeCaptureFixture(t, s, true)
	ins, binding, receipt := state.NativeBootTestAttempt(t, s, f, state.StateColdBooting)
	for _, query := range []string{
		`UPDATE instance_application_standard_admissions SET node_id=gen_random_uuid() WHERE instance_id=$1`,
		`UPDATE instance_application_standard_boots SET binding=jsonb_set(binding,'{payload_hash}','"forged"') WHERE instance_id=$1`,
		`DELETE FROM instance_application_standard_boots WHERE instance_id=$1`,
		`UPDATE instances SET state='running' WHERE id=$1`,
		`UPDATE instances SET netns='forged',host_ip='10.100.0.8',guest_uid=20008 WHERE id=$1`,
	} {
		_, err := pool.Exec(t.Context(), query, ins.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("raw mutation acquired authority: %s: %v", query, err)
		}
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM compute_nodes WHERE id=$1 FOR UPDATE`, binding.NodeID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateRunning, receipt); !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
		t.Fatalf("registered process fence waited/bypassed: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("child waited for process registration while holding parents")
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	state.AssertNativeBootUnpublished(t, s, ins)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateRunning, receipt); err != nil {
		t.Fatalf("retry after contention: %v", err)
	}
}
