package dbstorage

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStorageSamplesFollowPlacementAndFreshness(t *testing.T) {
	dsn := os.Getenv("FAAS_TEST_NATIVE_STORAGE_DSN")
	if dsn == "" {
		t.Skip("set FAAS_TEST_NATIVE_STORAGE_DSN to a disposable PostgreSQL database")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	migration, err := os.ReadFile("../../migrations/20261126223533201_native_postgres_storage_samples.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE TABLE database_vms (database_id uuid PRIMARY KEY, node_id text, standby_node text,
			standby_running boolean, state text, resize_pending boolean, state_changed_at timestamptz, volume_bytes bigint);`+
		string(migration)+`ALTER TABLE native_postgres_storage_samples ADD COLUMN low_since timestamptz;`)
	if err != nil {
		t.Fatal(err)
	}

	id := uuid.NewString()
	var pgID pgtype.UUID
	if err := pgID.Scan(id); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	runningSince := now.Add(-time.Minute)
	_, err = pool.Exec(ctx, "INSERT INTO database_vms VALUES ($1, 'db-a', 'db-b', true, 'running', false, $2, 1000)", id, runningSince)
	if err != nil {
		t.Fatal(err)
	}
	primary := Sample{DatabaseID: id, Role: "primary", NodeID: "db-a",
		StateChangedAt: runningSince, AllocatedBytes: 1000, TotalBytes: 980, FreeBytes: 100, Low: true, SampledAt: now}
	standby := primary
	standby.Role, standby.NodeID, standby.FreeBytes = "standby", "db-b", 60
	record := func(samples ...Sample) {
		t.Helper()
		if err := Record(ctx, pool, samples); err != nil {
			t.Fatal(err)
		}
	}
	read := func(at time.Time) map[string]Observation {
		t.Helper()
		samples, err := Read(ctx, pool, []pgtype.UUID{pgID}, at)
		if err != nil {
			t.Fatal(err)
		}
		byRole := make(map[string]Observation, len(samples))
		for _, s := range samples {
			byRole[s.Role] = s
		}
		return byRole
	}
	record(primary, standby)
	second := primary
	second.SampledAt = now.Add(time.Second)
	record(second)
	var lowSince time.Time
	if err := pool.QueryRow(ctx, "SELECT low_since FROM native_postgres_storage_samples WHERE role='primary'").Scan(&lowSince); err != nil || !lowSince.Equal(now) {
		t.Fatalf("consecutive low observations: %s, %v", lowSince, err)
	}
	older := primary
	older.FreeBytes, older.SampledAt = 500, now.Add(-time.Second)
	record(older)
	if p, s := read(now)["primary"], read(now)["standby"]; !p.Fresh || !s.Fresh || p.FreeBytes != 100 || s.FreeBytes != 60 {
		t.Fatalf("current samples: primary %+v standby %+v", p, s)
	}
	if got := read(now.Add(FreshFor + 2*time.Second))["primary"]; got.Fresh || got.FreeBytes != 100 {
		t.Fatalf("stale sample: %+v", got)
	}

	if _, err := pool.Exec(ctx, "UPDATE database_vms SET state='parked', standby_running=false, state_changed_at=$1", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	primary.FreeBytes, primary.SampledAt = 300, now.Add(2*time.Second)
	record(primary)
	if got := read(now.Add(2 * time.Second))["primary"]; got.Fresh || got.FreeBytes != 100 {
		t.Fatalf("parked sample: %+v", got)
	}
	if _, err := pool.Exec(ctx, "UPDATE database_vms SET state='running', node_id='db-b', standby_node=NULL, state_changed_at=$1", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := read(now.Add(3 * time.Second)); len(got) != 0 {
		t.Fatalf("new primary inherited a sample from another placement: %+v", got)
	}
	primary.NodeID, primary.StateChangedAt, primary.SampledAt = "db-b", now.Add(3*time.Second), now.Add(4*time.Second)
	record(primary)
	if got := read(now.Add(4 * time.Second))["primary"]; !got.Fresh || got.FreeBytes != 300 {
		t.Fatalf("new placement's sample: %+v", got)
	}
}
