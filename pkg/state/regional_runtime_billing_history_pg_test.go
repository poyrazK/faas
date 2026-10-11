//go:build !no_pg

package state

// adr: 799
import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPGRegionalRuntimeBillingHistoryContract(t *testing.T) {
	runRegionalRuntimeBillingHistoryContract(t, func(t *testing.T) (regionalCapacityFixture, Instance) {
		s, _, cell, account := releasePGFixture(t)
		return regionalAttemptFailureFixture(t, s, account, cell)
	})
}

func pgRegionalBillingHistoryInstance(t *testing.T) (*PgStore, Instance) {
	t.Helper()
	s, _, cell, account := releasePGFixture(t)
	return s, pgRegionalBillingHistoryCreateInstance(t, s, cell, account)
}

func pgRegionalBillingHistoryCreateInstance(t *testing.T, s *PgStore, cell string, account Account) Instance {
	t.Helper()
	q := newRegionalQuotaFixture(t, s, s, account, cell) // Independent app: no accepted quota head.
	dep, err := s.CreateDeployment(t.Context(), Deployment{AppID: q.grant.Binding.RegionalAppID, Kind: DeploymentKindImage, ImageDigest: "sha256:abc", Status: DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	region := "eu-fsn"
	node, err := s.CreateComputeNode(t.Context(), ComputeNode{ID: uuid.NewString(), Name: "billing-history-" + uuid.NewString(), TargetURL: "unix:///billing-history-test", Region: &region, Active: true,
		VPCPUs: 1, VCPUBudget: 1, MemMB: api.RAMAdmissionCeilingMB, AdmissionCeilingMB: api.RAMAdmissionCeilingMB, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(t.Context(), dep.AppID, dep.ID, string(StateColdBooting), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return ins
}

func TestPGRegionalRuntimeBillingHistoryTransitions(t *testing.T) {
	s, ins := pgRegionalBillingHistoryInstance(t)
	minute := ins.StartedAt.UTC().Truncate(time.Minute)
	original := regionalBillingHistoryRows(t, s, ins.AppID, minute)
	if len(original) != 1 {
		t.Fatal(original)
	}
	for _, statement := range []string{
		`UPDATE instances SET started_at=clock_timestamp(),state='running' WHERE id=$1`,
		`UPDATE instances SET state='draining' WHERE id=$1`,
		`UPDATE instances SET state='snapshotting' WHERE id=$1`,
		`UPDATE instances SET state='migrating' WHERE id=$1`,
		`UPDATE instances SET state='warm' WHERE id=$1`,
	} {
		if _, err := s.pool.Exec(t.Context(), statement, ins.ID); err != nil {
			t.Fatal(err)
		}
	}
	rows := regionalBillingHistoryRows(t, s, ins.AppID, minute)
	if len(rows) != 1 || !reflect.DeepEqual(rows, original) {
		t.Fatal("live-state changes reset original billing history", rows, original)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE instances SET state='failed' WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE instances SET state='cold_booting' WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE instances SET wake_id=gen_random_uuid() WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM instances WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	rows = regionalBillingHistoryRows(t, s, ins.AppID, minute)
	if len(rows) != 3 || rows[0].EndedAt == nil || rows[0].UnknownSince != nil || rows[1].UnknownSince == nil || rows[1].EndedAt != nil || rows[2].UnknownSince == nil || rows[2].EndedAt != nil {
		t.Fatal("closure, replacement or collection lost unresolved usage", rows)
	}
	restarted := NewPgStore(s.pool)
	if got := regionalBillingHistoryRows(t, restarted, ins.AppID, minute); !reflect.DeepEqual(got, rows) {
		t.Fatal("restart lost retained history", got, rows)
	}
	for _, row := range rows {
		page, err := s.ListRegionalRuntimeBillingIntervals(t.Context(), ins.AppID, minute, row.ID-1, 1)
		if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0], row) {
			t.Fatal("history pagination", page, err)
		}
	}
	if later := regionalBillingHistoryRows(t, s, ins.AppID, minute.Add(time.Minute)); len(later) != 2 {
		t.Fatal("unknown stop disappeared in subsequent minute", later)
	}
}

func TestPGRegionalRuntimeBillingHistoryRollbackAndMirror(t *testing.T) {
	s, ins := pgRegionalBillingHistoryInstance(t)
	minute := ins.StartedAt.UTC().Truncate(time.Minute)
	original := regionalBillingHistoryRows(t, s, ins.AppID, minute)
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `UPDATE instances SET state='failed' WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := regionalBillingHistoryRows(t, s, ins.AppID, minute); !reflect.DeepEqual(got, original) {
		t.Fatal("rolled-back transition left billing history", got)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE instances SET mode='mirror' WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	closed := regionalBillingHistoryRows(t, s, ins.AppID, minute)
	if len(closed) != 1 || closed[0].EndedAt == nil || closed[0].UnknownSince != nil {
		t.Fatal("mirror transition did not close billable interval", closed)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE instances SET wake_id=gen_random_uuid(),started_at=clock_timestamp() WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	if got := regionalBillingHistoryRows(t, s, ins.AppID, minute); !reflect.DeepEqual(got, closed) {
		t.Fatal("mirror created compute history", got)
	}
}

func TestPGRegionalRuntimeBillingHistorySQLGuards(t *testing.T) {
	s, ins := pgRegionalBillingHistoryInstance(t)
	minute := ins.StartedAt.UTC().Truncate(time.Minute)
	original := regionalBillingHistoryRows(t, s, ins.AppID, minute)
	for _, statement := range []string{
		`UPDATE regional_runtime_billing_intervals SET started_at=started_at-interval '1 second'`,
		`UPDATE regional_runtime_billing_intervals SET ended_at=clock_timestamp()`,
		`UPDATE regional_runtime_billing_intervals SET unknown_since=clock_timestamp()`,
		`UPDATE regional_runtime_billing_intervals SET run_id=gen_random_uuid()`,
		`UPDATE regional_runtime_billing_intervals SET complete_start=false`,
		`DELETE FROM regional_runtime_billing_intervals`,
		`TRUNCATE regional_runtime_billing_intervals`,
		`UPDATE regional_runtime_billing_history_start SET started_at=started_at-interval '1 day'`,
		`DELETE FROM regional_runtime_billing_history_start`,
		`TRUNCATE regional_runtime_billing_history_start`,
	} {
		if _, err := s.pool.Exec(t.Context(), statement); err == nil {
			t.Fatal("direct mutation bypassed lifecycle history", statement)
		}
	}
	if got := regionalBillingHistoryRows(t, s, ins.AppID, minute); !reflect.DeepEqual(got, original) {
		t.Fatal("failed SQL mutation changed history", got)
	}
	_, down := regionalBillingHistoryMigration(t)
	if _, err := s.pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot discard retained") {
		t.Fatal("populated downgrade discarded history", err)
	}
}

func TestPGRegionalRuntimeBillingHistoryJournalLock(t *testing.T) {
	s, ins := pgRegionalBillingHistoryInstance(t)
	writer, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(t.Context()) }()
	if _, err := writer.Exec(t.Context(), `UPDATE instances SET state='failed' WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	reader, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(t.Context()) }()
	var locked bool
	const attempt = `SELECT pg_try_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||$1::uuid::text,0))`
	if err := reader.QueryRow(t.Context(), attempt, ins.AppID).Scan(&locked); err != nil || locked {
		t.Fatal("billing closure escaped the app journal lock", locked, err)
	}
	if err := writer.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRow(t.Context(), attempt, ins.AppID).Scan(&locked); err != nil || !locked {
		t.Fatal("journal lock survived rollback", locked, err)
	}
}

func regionalBillingHistoryMigration(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261011201627001_regional_runtime_billing_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing migration downgrade")
	}
	return up, down
}

func TestPGRegionalRuntimeBillingHistoryMigrationBoundary(t *testing.T) {
	s, _, cell, account := releasePGFixture(t)
	up, down := regionalBillingHistoryMigration(t)
	if _, err := s.pool.Exec(t.Context(), down); err != nil {
		t.Fatal("empty downgrade", err)
	}
	// Use this same migrated database while the new journal is absent.
	ins := pgRegionalBillingHistoryCreateInstance(t, s, cell, account)
	if _, err := s.pool.Exec(t.Context(), up); err != nil {
		t.Fatal("empty upgrade", err)
	}
	start, err := s.RegionalRuntimeBillingHistoryStart(t.Context())
	if err != nil || start.IsZero() {
		t.Fatal("installation time not retained", start, err)
	}
	rows := regionalBillingHistoryRows(t, s, ins.AppID, start.UTC().Truncate(time.Minute))
	if len(rows) != 1 || rows[0].CompleteStart || !rows[0].StartedAt.Equal(start) || !rows[0].RecordedAt.Equal(start) || rows[0].SourceMatches != 0 {
		t.Fatal("upgrade fabricated historical billing coverage", rows)
	}
}
