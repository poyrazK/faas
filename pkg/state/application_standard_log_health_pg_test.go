//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgApplicationStandardLogHealth(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	standardLogHealthLifecycle(t, s)
}

func TestPgApplicationStandardLogHealthExceptionDeadline(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	standardLogHealthExceptionDeadline(t, s)
}

func TestPgApplicationStandardLogHealthNonwaitingAndRawGuards(t *testing.T) {
	s, p := state.StandardOperationPGStore(t)
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	c := state.StandardLogInventorySession(t, s, state.StandardLogInventoryNode(t, s).ID)
	d := state.StandardLogDeliveryDrain(t, s, f)
	e := state.ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "unknown", Reason: "idle"}
	standardLogHealthWrite(t, s, c, d, e)
	for _, query := range []string{
		`SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.'||$1::uuid::text,0))`,
		`SELECT app_id FROM app_application_standards WHERE app_id=$1 FOR UPDATE`,
		`SELECT app_id FROM application_standard_log_health WHERE app_id=$1 FOR UPDATE`,
	} {
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, query, (*f.App()).ID); err != nil {
			t.Fatal(err)
		}
		_, err = s.RecordApplicationStandardLogHealth(ctx, c, d, e)
		_ = tx.Rollback(ctx)
		if !errors.Is(err, state.ErrApplicationStandardReviewBusy) {
			t.Fatalf("storage waited on mutation: %v", err)
		}
	}
	for _, query := range []string{
		`UPDATE application_standard_log_health SET binding=jsonb_set(binding,'{drain_config_hash}','"wrong"') WHERE app_id=$1`,
		`UPDATE application_standard_log_health SET source_instance_id='00000000-0000-0000-0000-000000000001',sequence=1,status='healthy',reason='delivered',event_revision=2 WHERE app_id=$1`,
	} {
		if _, err := p.Exec(ctx, query, (*f.App()).ID); !errors.Is(state.StandardLogHealthPGError(err), state.ErrApplicationStandardLogDeliveryStale) {
			t.Fatalf("raw stale tuple accepted: %v", err)
		}
	}
	if _, err := p.Exec(ctx, `UPDATE application_standard_log_health SET session_id=$2 WHERE app_id=$1`, (*f.App()).ID, uuid.NewString()); !errors.Is(state.StandardLogHealthPGError(err), state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("raw wrong session accepted: %v", err)
	}
	if _, err := p.Exec(ctx, `UPDATE application_standard_log_health SET status='degraded',reason='retrying',event_revision=2 WHERE app_id=$1`, (*f.App()).ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE application_standard_log_health SET status='unknown',reason='idle',event_revision=1 WHERE app_id=$1`, (*f.App()).ID); !errors.Is(state.StandardLogHealthPGError(err), state.ErrApplicationStandardLogHealthStale) {
		t.Fatal("raw older event overwrote current health")
	}
	if _, err := p.Exec(ctx, `UPDATE application_standard_log_health SET event_at=$2,observed_at=$2 WHERE app_id=$1`, (*f.App()).ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListApplicationStandardLogHealth(ctx, (*f.Owner()).PersonalOrg.ID, (*f.App()).ID)
	if err != nil || len(rows) != 1 || rows[0].EventAt.After(time.Now()) || rows[0].ObservedAt.After(time.Now()) {
		t.Fatal("caller supplied health clocks")
	}
	standardLogHealthAgePGFixture(t, s, f)
	if _, err := p.Exec(ctx, `DELETE FROM compute_nodes WHERE id=$1`, c.NodeID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM application_standard_log_health WHERE app_id=$1`, (*f.App()).ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("node cascade retained private health")
	}
}

func standardLogHealthAgePGFixture(t *testing.T, s *state.PgStore, f state.StandardLocalIntentFixture) {
	t.Helper()
	tx, err := (*s.Pool()).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	for _, query := range []string{
		`ALTER TABLE application_standard_log_health DISABLE TRIGGER application_standard_log_health_current`,
		`UPDATE application_standard_log_health SET observed_at=clock_timestamp()-make_interval(secs=>$1)`,
		`ALTER TABLE application_standard_log_health ENABLE TRIGGER application_standard_log_health_current`,
	} {
		var args []any
		if query[0] == 'U' {
			args = []any{api.ApplicationStandardLogHealthFreshness.Seconds() + 1}
		}
		if _, err := tx.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	standardLogHealthRead(t, s, f, 0, "")
}
