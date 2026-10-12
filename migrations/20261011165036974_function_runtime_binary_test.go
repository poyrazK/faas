package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 987
func TestFunctionRuntimeBinaryMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	accepts := func(table, column, value string) bool {
		t.Helper()
		var ok bool
		query := `SELECT bool_and(pg_get_constraintdef(oid) LIKE '%''' || $3 || '''%')
			FROM pg_constraint WHERE conrelid = $1::regclass AND conname = $2`
		if err := pool.QueryRow(t.Context(), query, table, table+"_"+column+"_check", value).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !accepts("apps", "runtime", "binary") || !accepts("request_telemetry", "guest_runtime", "binary") {
		t.Fatal("binary is not accepted by the runtime CHECKs after Up")
	}
	for _, older := range []string{"node22", "python312", "go124", "go124-alpine", "node24", "python313"} {
		if !accepts("apps", "runtime", older) {
			t.Fatalf("Up dropped the existing runtime %q", older)
		}
	}

	data, err := migrations.FS.ReadFile("20261011165036974_function_runtime_binary.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "-- +goose Down", 2)
	if _, err := pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal("rollback", err)
	}
	if accepts("apps", "runtime", "binary") || accepts("request_telemetry", "guest_runtime", "binary") {
		t.Fatal("binary still accepted after Down")
	}
	for range 2 {
		if _, err := pool.Exec(t.Context(), parts[0]); err != nil {
			t.Fatal("replay", err)
		}
	}
	if !accepts("apps", "runtime", "binary") {
		t.Fatal("binary not accepted after replaying Up")
	}
}
