//go:build !no_pg

package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestCheckedBindingRetirementMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	source, err := os.ReadFile("20261018014811962_checked_binding_retirement.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	if _, err := pool.Exec(t.Context(), down); err != nil {
		t.Fatal("empty-history downgrade", err)
	}
	if _, err := pool.Exec(t.Context(), up); err != nil {
		t.Fatal("upgrade", err)
	}
	var guard bool
	if err := pool.QueryRow(t.Context(), `SELECT to_regprocedure('enforce_binding_release_graph()') IS NOT NULL`).Scan(&guard); err != nil || !guard {
		t.Fatal("original binding enforcement lost", err)
	}
	if _, err := pool.Exec(t.Context(), `SELECT authorize_environment_binding_retirement('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','','')`); err == nil {
		t.Fatal("nonexistent source authorized retirement")
	}
}
