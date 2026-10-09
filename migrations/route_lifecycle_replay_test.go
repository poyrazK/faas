//go:build !no_pg

// adr: 823
package migrations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestRouteLifecycleReplayPreservesProjectSuccessorWrapper(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	paths, err := fs.Glob(migrations.FS, "*_release_graph_lifecycle_successors.sql")
	if err != nil || len(paths) != 1 {
		t.Fatalf("graph migration: %v %v", paths, err)
	}
	version, err := strconv.ParseInt(strings.SplitN(paths[0], "_", 2)[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	const query = `SELECT lifecycle_successor_bindings('00000000-0000-0000-0000-000000000001'::uuid, '[{"method":"GET","path":"/retired"}]'::jsonb)`
	var before []byte
	if err := pool.QueryRow(ctx, query).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var bindings []map[string]json.RawMessage
	if err := json.Unmarshal(before, &bindings); err != nil || len(bindings) != 1 {
		t.Fatalf("bindings: %s %v", before, err)
	}
	if _, ok := bindings[0]["project"]; !ok {
		t.Fatalf("project binding wrapper missing: %s", before)
	}
	if tag, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=$1`, version); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("remove graph ledger row: %v %v", tag, err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err := pool.QueryRow(ctx, query).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("replay changed successor bindings: before=%s after=%s", before, after)
	}
}
