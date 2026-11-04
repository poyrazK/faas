package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// A concurrent index build that fails part way leaves an INVALID index, and
// CREATE INDEX CONCURRENTLY IF NOT EXISTS would keep it forever. MigrateUp
// must fail and name it instead of reporting success (bug hunt H73).
func TestMigrateUpFailsOnAnInvalidIndex(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckNoInvalidIndexes(ctx, pool); err != nil {
		t.Fatalf("fresh schema: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS h73_dupes`) })
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS h73_dupes`,
		`CREATE TABLE h73_dupes (v int)`,
		`INSERT INTO h73_dupes VALUES (1), (1)`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	// The duplicate makes the unique build fail after its catalog entry exists.
	if _, err := pool.Exec(ctx, `CREATE UNIQUE INDEX CONCURRENTLY h73_dupes_v ON h73_dupes (v)`); err == nil {
		t.Fatal("unique build over duplicates succeeded")
	}
	err := db.MigrateUp(ctx, pool)
	if !errors.Is(err, db.ErrInvalidIndexes) || !strings.Contains(err.Error(), "h73_dupes_v") {
		t.Fatalf("MigrateUp with an invalid index = %v, want ErrInvalidIndexes naming it", err)
	}
	if _, err := pool.Exec(ctx, `DROP INDEX h73_dupes_v`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("after dropping it: %v", err)
	}
}
