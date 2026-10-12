package state

// adr: 801
import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
)

func TestPGRegionalRuntimeFloorHistoryContract(t *testing.T) {
	runRegionalFloorHistoryContract(t, func(t *testing.T) regionalFloorFixture {
		s, _, _, a := releasePGFixture(t)
		return newRegionalFloorFixture(t, s, a)
	})
}

func regionalFloorHistoryMigration(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261011225500001_regional_runtime_floor_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	return up, down
}

func TestPGRegionalRuntimeFloorHistoryRollbackAndJournalLock(t *testing.T) {
	s, _, _, a := releasePGFixture(t)
	f := newRegionalFloorFixture(t, s, a)
	before := floorHistoryRows(t, s, f.app.ID, f.app.CreatedAt)
	for _, statement := range []string{`UPDATE apps SET min_instances=1 WHERE id=$1`, `UPDATE deployments SET min_instances=2 WHERE app_id=$1`} {
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), statement, f.app.ID); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
		reader, err := s.pool.Begin(t.Context())
		if err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
		var acquired bool
		const lock = `SELECT pg_try_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||$1::uuid::text,0))`
		if err = reader.QueryRow(t.Context(), lock, f.app.ID).Scan(&acquired); err != nil || acquired {
			_ = reader.Rollback(t.Context())
			_ = tx.Rollback(t.Context())
			t.Fatal("floor writer escaped quota journal lock", acquired, err)
		}
		if err = tx.Rollback(t.Context()); err != nil {
			_ = reader.Rollback(t.Context())
			t.Fatal(err)
		}
		if err = reader.QueryRow(t.Context(), lock, f.app.ID).Scan(&acquired); err != nil || !acquired {
			_ = reader.Rollback(t.Context())
			t.Fatal("rollback retained floor lock", acquired, err)
		}
		if err = reader.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := floorHistoryRows(t, s, f.app.ID, f.app.CreatedAt); !reflect.DeepEqual(got, before) {
			t.Fatal("rollback rewrote floor intent", got)
		}
	}
}

func TestPGRegionalRuntimeFloorHistoryGuardsAndRestart(t *testing.T) {
	s, _, _, a := releasePGFixture(t)
	f := newRegionalFloorFixture(t, s, a)
	before := floorHistoryRows(t, s, f.app.ID, f.app.CreatedAt)
	for _, statement := range []string{
		`DELETE FROM regional_runtime_floor_intervals WHERE app_id=$1`,
		`UPDATE regional_runtime_floor_intervals SET ended_at=clock_timestamp() WHERE app_id=$1`,
		`UPDATE regional_runtime_floor_intervals SET state_claims='{}' WHERE app_id=$1`,
		`INSERT INTO regional_runtime_floor_intervals(app_id,object_id,kind,started_at,state_claims) VALUES($1,$1,'app',clock_timestamp(),'{}')`,
	} {
		if _, err := s.pool.Exec(t.Context(), statement, f.app.ID); err == nil {
			t.Fatal("history edit accepted", statement)
		}
	}
	for _, statement := range []string{`TRUNCATE apps CASCADE`, `TRUNCATE deployments CASCADE`, `TRUNCATE regional_runtime_floor_intervals`, `DELETE FROM regional_runtime_floor_history_start`, `UPDATE regional_runtime_floor_history_start SET started_at=clock_timestamp()`, `TRUNCATE regional_runtime_floor_history_start`} {
		if _, err := s.pool.Exec(t.Context(), statement); err == nil {
			t.Fatal("coverage boundary edit accepted", statement)
		}
	}
	_, down := regionalFloorHistoryMigration(t)
	if _, err := s.pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot discard retained") {
		t.Fatal("populated downgrade", err)
	}
	if got := floorHistoryRows(t, NewPgStore(s.pool), f.app.ID, f.app.CreatedAt); !reflect.DeepEqual(got, before) {
		t.Fatal("restart or edit lost history", got)
	}
}

func TestPGRegionalRuntimeFloorHistoryInstallationBoundary(t *testing.T) {
	s, _, _, a := releasePGFixture(t)
	up, down := regionalFloorHistoryMigration(t)
	if _, err := s.pool.Exec(t.Context(), down); err != nil {
		t.Fatal("empty downgrade", err)
	}
	f := newRegionalFloorFixture(t, s, a)
	if err := s.SetAppMinInstances(t.Context(), f.app.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), up); err != nil {
		t.Fatal("upgrade", err)
	}
	start, err := s.RegionalRuntimeFloorHistoryStart(t.Context())
	if err != nil || start.IsZero() {
		t.Fatal(start, err)
	}
	rows := floorHistoryRows(t, s, f.app.ID, start)
	if len(rows) != 2 || !rows[0].StartedAt.Equal(start) || !rows[1].StartedAt.Equal(start) || rows[0].App.MinInstances != 2 {
		t.Fatal("installation fabricated earlier floor coverage", rows)
	}
	before := start.UTC().Truncate(time.Minute).Add(-time.Minute)
	if rows, err := s.ListRegionalRuntimeFloorIntervals(t.Context(), f.app.ID, before, 0, 10); err != nil || len(rows) != 0 {
		t.Fatal("pre-installation history fabricated", rows, err)
	}
}

func TestPGRegionalRuntimeFloorHistoryReparentAndTimezone(t *testing.T) {
	s, _, _, a := releasePGFixture(t)
	f := newRegionalFloorFixture(t, s, a)
	other := newRegionalFloorFixture(t, s, a)
	before := floorHistoryRows(t, s, f.app.ID, f.app.CreatedAt)
	// Timestamp serialization must not manufacture policy changes when a
	// connection timezone changes. The original creation ordering stays UTC.
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), `SET LOCAL TIME ZONE 'America/New_York'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `UPDATE deployments SET error='metadata only' WHERE id=$1`, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := floorHistoryRows(t, s, f.app.ID, f.app.CreatedAt); !reflect.DeepEqual(got, before) {
		t.Fatal("connection timezone rewrote floor", got)
	}
	tx, err = s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), `UPDATE deployments SET app_id=$1,revision=2 WHERE id=$2`, other.app.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	reader, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(t.Context()) }()
	for _, app := range []string{f.app.ID, other.app.ID} {
		var acquired bool
		if err = reader.QueryRow(t.Context(), `SELECT pg_try_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||$1::uuid::text,0))`, app).Scan(&acquired); err != nil || acquired {
			t.Fatal("reparent escaped original/new app root", app, acquired, err)
		}
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	old := floorHistoryRows(t, s, f.app.ID, f.app.CreatedAt)
	next := floorHistoryRows(t, s, other.app.ID, other.app.CreatedAt)
	if len(old) != 2 || old[1].EndedAt == nil || len(next) != 3 || next[2].ObjectID != regionalQuotaCanonicalID(f.dep.ID) || next[2].AppID != other.app.ID || !next[2].StartedAt.Equal(*old[1].EndedAt) {
		t.Fatal("reparent lost original floor attribution", old, next)
	}
}
