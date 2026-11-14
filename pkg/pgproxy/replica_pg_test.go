package pgproxy

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestReadOnlyEndpointsRefuseWritesOnThePrimary connects to a database with
// no replica through <id>-ro and <id>-ro-pooler: reads work and writes fail
// as they would on a standby, while <id> still writes.
func TestReadOnlyEndpointsRefuseWritesOnThePrimary(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	table := "ro_probe_" + uuid.NewString()[:8]
	if _, err := h.backend.Exec(ctx, "CREATE TABLE "+table+" (id int); GRANT SELECT, INSERT, DELETE ON "+table+" TO "+role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.backend.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	writer, err := h.connect(ctx, databaseID+"."+testSuffix, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatalf("direct endpoint: %v", err)
	}
	defer func() { _ = writer.Close(ctx) }()
	if _, err := writer.Exec(ctx, "INSERT INTO "+table+" VALUES (1)"); err != nil {
		t.Fatalf("write through the direct endpoint: %v", err)
	}

	for _, label := range []string{databaseID + readOnlySuffix, databaseID + readOnlySuffix + pooledSuffix} {
		t.Run(label[len(databaseID):], func(t *testing.T) {
			reader, err := h.connect(ctx, label+"."+testSuffix, role, password, h.dbName, "require", nil)
			if err != nil {
				t.Fatalf("read-only endpoint: %v", err)
			}
			defer func() { _ = reader.Close(ctx) }()
			var n int
			if err := reader.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 1 {
				t.Fatalf("read = %d, %v; want 1 row", n, err)
			}
			if _, err := reader.Exec(ctx, "INSERT INTO "+table+" VALUES (2)"); sqlState(err) != "25006" {
				t.Fatalf("write through %s = %v, want 25006 (read-only transaction)", label, err)
			}
		})
	}
	// The no-SNI form names the read-only endpoint too.
	reader, err := h.connect(ctx, "127.0.0.1", role, password, h.dbName, "require", map[string]string{"options": "endpoint=" + databaseID + readOnlySuffix})
	if err != nil {
		t.Fatalf("read-only endpoint by option: %v", err)
	}
	defer func() { _ = reader.Close(ctx) }()
	if _, err := reader.Exec(ctx, "DELETE FROM "+table); sqlState(err) != "25006" {
		t.Fatalf("write by option = %v, want 25006", err)
	}
}
