package dbmigration

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestAssessReportsEveryVisibleBlocker(t *testing.T) {
	source := Source{Major: 16, Writable: true, LogicalWAL: true, CanPublish: true, CanReplicate: true,
		FreeSlots: 3, FreeSenders: 3, RetentionBytes: MaxRetentionBytes, Tables: 2, Sequences: 1,
		DatabaseBytes: 100}
	target := Target{Major: 17, Empty: true, FreeBytes: 125, StorageReserveBytes: 25, HasWorkerCapacity: true}
	if got := Assess(source, target); got.Outcome != Compatible || len(got.Reasons) != 0 || got.SequenceCount != 1 {
		t.Fatalf("compatible result = %+v", got)
	}
	source.FreeSlots, source.RetentionBytes, source.UnsupportedCount, source.LargeObjects = 2, -1, 3, true
	source.UnsupportedTables = []string{"public.z", "public.\nnot a table", "public.a"}
	target.Empty, target.FreeBytes = false, 124
	got := Assess(source, target)
	want := []string{"source_replication_slots_capacity", "source_wal_retention_limit_required", "source_tables_unsupported",
		"source_large_objects_unsupported", "target_not_empty", "target_storage_insufficient"}
	if got.Outcome != Blocked || !slices.Equal(got.Reasons, want) || !slices.Equal(got.UnsupportedTables, []string{"public.a", "public.z"}) ||
		got.UnsupportedTableCount != 3 {
		t.Fatalf("blocked result = %+v", got)
	}
}

// The fixture uses a non-superuser source and real PostgreSQL 16 catalogs.
func TestInspectSourceSQL(t *testing.T) {
	dsn := os.Getenv("FAAS_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set FAAS_MIGRATION_TEST_DSN to a disposable PostgreSQL 16 migration source")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	read := func() Source {
		t.Helper()
		var raw []byte
		if err := conn.QueryRow(ctx, InspectSourceSQL).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var source Source
		if err := json.Unmarshal(raw, &source); err != nil {
			t.Fatal(err)
		}
		return source
	}
	before := read()
	if before.Major != 16 || !before.Writable || !before.LogicalWAL || !before.CanPublish || !before.CanReplicate ||
		before.Tables != 2 || before.UnsupportedCount != 1 || !slices.Equal(before.UnsupportedTables, []string{"public.unsupported"}) ||
		before.Sequences != 1 || before.DatabaseBytes <= 0 {
		t.Fatalf("source evidence = %+v", before)
	}
	if after := read(); !reflect.DeepEqual(before, after) {
		t.Fatalf("repeated check changed source evidence: before %+v, after %+v", before, after)
	}
	var slots, publications int
	if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_replication_slots), (SELECT count(*) FROM pg_publication)`).Scan(&slots, &publications); err != nil || slots != 0 || publications != 0 {
		t.Fatalf("inspection created replication resources: slots %d, publications %d, error %v", slots, publications, err)
	}
}
