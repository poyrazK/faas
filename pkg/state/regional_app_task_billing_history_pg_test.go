//go:build !no_pg

package state

// adr: 800
import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
)

func TestPGRegionalAppTaskBillingHistoryContract(t *testing.T) {
	runRegionalAppTaskBillingHistoryContract(t, func(t *testing.T, kind AppTaskKind, running bool) regionalTaskBillingFixture {
		s, _, cell, account := releasePGFixture(t)
		return newRegionalTaskBillingFixture(t, s, account, cell, kind, running)
	})
}

func TestPGRegionalAppTaskBillingRecoveryContract(t *testing.T) {
	runRegionalAppTaskBillingRecoveryContract(t, func(t *testing.T) quotaWriteFixture {
		s, _, cell, account := releasePGFixture(t)
		return newQuotaWriteFixture(t, s, account, cell)
	})
}

func pgTaskBillingClaim(t *testing.T, s *PgStore, cell string, account Account) AppTask {
	t.Helper()
	f := newQuotaWriteFixture(t, s, account, cell)
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	task := f.task(t, base, nil)
	claim, err := s.ClaimNextAppTask(t.Context(), "pg-billing-scheduler", base.Add(time.Second), time.Hour)
	if err != nil || claim.ID != task.ID {
		t.Fatal(claim, err)
	}
	return claim
}

func TestPGRegionalAppTaskBillingDeletionReplacementAndRestart(t *testing.T) {
	s, _, cell, account := releasePGFixture(t)
	claim := pgTaskBillingClaim(t, s, cell, account)
	minute := claim.UpdatedAt.Truncate(time.Minute)
	before := taskBillingRows(t, s, claim.AppID, minute)
	if _, err := s.pool.Exec(t.Context(), `UPDATE app_tasks SET lease_token=gen_random_uuid(),lease_owner='replacement-scheduler',updated_at=clock_timestamp() WHERE id=$1`, claim.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM app_tasks WHERE id=$1`, claim.ID); err != nil {
		t.Fatal(err)
	}
	rows := taskBillingRows(t, s, claim.AppID, time.Now().UTC().Truncate(time.Minute).Add(time.Minute))
	if len(rows) != 2 || rows[0].RunID != before[0].RunID || rows[1].RunID == rows[0].RunID || rows[0].UnknownSince == nil || rows[1].UnknownSince == nil || rows[0].EndedAt != nil || rows[1].EndedAt != nil {
		t.Fatal("replacement/delete lost unresolved task usage", rows)
	}
	if got := taskBillingRows(t, NewPgStore(s.pool), claim.AppID, time.Now().UTC().Truncate(time.Minute).Add(time.Minute)); !reflect.DeepEqual(got, rows) {
		t.Fatal("restart lost task history", got)
	}
}

func TestPGRegionalAppTaskBillingRollbackAndJournalLock(t *testing.T) {
	s, _, cell, account := releasePGFixture(t)
	claim := pgTaskBillingClaim(t, s, cell, account)
	minute := claim.UpdatedAt.Truncate(time.Minute)
	before := taskBillingRows(t, s, claim.AppID, minute)
	writer, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(t.Context()) }()
	if _, err = writer.Exec(t.Context(), `UPDATE app_tasks SET status='cancelled',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, claim.ID); err != nil {
		t.Fatal(err)
	}
	reader, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(t.Context()) }()
	const attempt = `SELECT pg_try_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||$1::uuid::text,0))`
	var locked bool
	if err = reader.QueryRow(t.Context(), attempt, claim.AppID).Scan(&locked); err != nil || locked {
		t.Fatal("task billing escaped app journal lock", locked, err)
	}
	if err = writer.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = reader.QueryRow(t.Context(), attempt, claim.AppID).Scan(&locked); err != nil || !locked {
		t.Fatal("task journal lock survived rollback", locked, err)
	}
	if got := taskBillingRows(t, s, claim.AppID, minute); !reflect.DeepEqual(got, before) {
		t.Fatal("rolled-back task wrote billing history", got)
	}
}

func taskBillingMigration(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261011214310001_regional_app_task_billing_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing migration downgrade")
	}
	return up, down
}

func TestPGRegionalAppTaskBillingSQLGuards(t *testing.T) {
	s, _, cell, account := releasePGFixture(t)
	claim := pgTaskBillingClaim(t, s, cell, account)
	minute := claim.UpdatedAt.Truncate(time.Minute)
	before := taskBillingRows(t, s, claim.AppID, minute)
	for _, statement := range []string{
		`UPDATE regional_app_task_billing_intervals SET started_at=started_at-interval '1 second'`,
		`UPDATE regional_app_task_billing_intervals SET ended_at=clock_timestamp()`,
		`UPDATE regional_app_task_billing_intervals SET unknown_since=clock_timestamp()`,
		`UPDATE regional_app_task_billing_intervals SET lease_owner='new-owner'`,
		`UPDATE regional_app_task_billing_intervals SET run_id=gen_random_uuid()`,
		`UPDATE regional_app_task_billing_intervals SET complete_start=false`,
		`DELETE FROM regional_app_task_billing_intervals`,
		`TRUNCATE regional_app_task_billing_intervals`,
		`UPDATE regional_app_task_billing_history_start SET started_at=started_at-interval '1 day'`,
		`DELETE FROM regional_app_task_billing_history_start`,
		`TRUNCATE regional_app_task_billing_history_start`,
	} {
		if _, err := s.pool.Exec(t.Context(), statement); err == nil {
			t.Fatal("direct mutation bypassed task history", statement)
		}
	}
	if got := taskBillingRows(t, s, claim.AppID, minute); !reflect.DeepEqual(got, before) {
		t.Fatal("failed SQL mutation changed task history", got)
	}
	_, down := taskBillingMigration(t)
	if _, err := s.pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot discard retained") {
		t.Fatal("populated downgrade discarded task history", err)
	}
}

func TestPGRegionalAppTaskBillingMigrationBoundary(t *testing.T) {
	s, _, cell, account := releasePGFixture(t)
	up, down := taskBillingMigration(t)
	if _, err := s.pool.Exec(t.Context(), down); err != nil {
		t.Fatal("empty task downgrade", err)
	}
	claim := pgTaskBillingClaim(t, s, cell, account)
	if _, err := s.pool.Exec(t.Context(), up); err != nil {
		t.Fatal("task upgrade", err)
	}
	start, err := s.RegionalAppTaskBillingHistoryStart(t.Context())
	if err != nil || start.IsZero() {
		t.Fatal(start, err)
	}
	rows := taskBillingRows(t, s, claim.AppID, start.UTC().Truncate(time.Minute))
	if len(rows) != 1 || rows[0].CompleteStart || !rows[0].StartedAt.Equal(start) || !rows[0].RecordedAt.Equal(start) || rows[0].RunID != *claim.LeaseToken || rows[0].SourceMatches != 0 {
		t.Fatal("installation fabricated preexisting task coverage", rows)
	}
}
