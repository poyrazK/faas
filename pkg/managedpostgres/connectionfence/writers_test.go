package connectionfence

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestConnectionFenceWriterInventoryDetectsCatalogBypassAndHiddenSessions(t *testing.T) {
	f := newFixture(t)
	client, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.c.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	closed, err := f.c.Close(t.Context(), f.request)
	if err != nil || closed.WriterInventory == nil || !closed.WriterInventory.CatalogComplete || closed.WriterInventory.UnknownBackends == 0 {
		t.Fatal("hidden backend activity was presented as an empty source", err)
	}
	if _, err = f.connect(t.Context(), t, f.request.DatabaseNames[0], f.tenant); err == nil {
		t.Fatal("source reconnect bypassed connection admission")
	}
	if _, err = client.Exec(t.Context(), "SELECT 1"); err != nil {
		t.Fatal("admission closure unexpectedly terminated an existing writer", err)
	}
	if _, err = f.root.Exec(t.Context(), "GRANT pg_read_all_stats TO "+pgx.Identifier{f.admin}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	visible, err := f.c.Observe(t.Context(), f.request.Identity)
	if err != nil || visible.WriterInventory.ClientSessions < 2 || visible.WriterInventory.UnknownBackends >= closed.WriterInventory.UnknownBackends {
		t.Fatal("writer activity did not reflect independently granted visibility", err)
	}
	name := f.request.DatabaseNames[0] + "_late"
	if _, err = f.root.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" OWNER "+pgx.Identifier{f.admin}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal("fixture could not demonstrate the SQL-only catalog bypass", err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := f.root.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	// Other packages create and drop their own databases on this cluster. Hold
	// catalog DDL while checking against the current set, rather than assuming
	// the count from Close can only have changed because of our new database.
	catalog, err := f.root.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = catalog.Rollback(context.Background()) }()
	if _, err = catalog.Exec(t.Context(), "LOCK TABLE pg_catalog.pg_database IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	var unselected int64
	if err = catalog.QueryRow(t.Context(), "SELECT count(*) FROM pg_catalog.pg_database WHERE datname<>$1 AND datname<>ALL($2::text[])",
		f.config.MaintenanceDatabase, f.request.DatabaseNames).Scan(&unselected); err != nil {
		t.Fatal(err)
	}
	changed, err := f.c.Observe(t.Context(), f.request.Identity)
	if err != nil || changed.WriterInventory.CatalogFingerprint == closed.WriterInventory.CatalogFingerprint ||
		changed.UnselectedDatabases != unselected {
		t.Fatalf("new database hid behind a frozen selected set: observation=%+v want_unselected=%d err=%v", changed, unselected, err)
	}
	if err = catalog.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.root.Exec(t.Context(), "ALTER DATABASE "+pgx.Identifier{name}.Sanitize()+" CONNECTION LIMIT 7"); err != nil {
		t.Fatal(err)
	}
	settings, err := f.c.Observe(t.Context(), f.request.Identity)
	if err != nil || settings.WriterInventory.CatalogFingerprint == changed.WriterInventory.CatalogFingerprint {
		t.Fatal("database settings drift reused catalog evidence", err)
	}
}
