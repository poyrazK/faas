//go:build !no_pg

package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestCandidateBindingQualificationMigrationRoundTrip(t *testing.T) {
	pool := openMigrated(t)
	source, err := os.ReadFile("20261017200000001_candidate_binding_qualification.sql")
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
	var originalGuard bool
	if err := pool.QueryRow(t.Context(), "SELECT to_regprocedure('enforce_binding_release_graph()') IS NOT NULL").Scan(&originalGuard); err != nil || !originalGuard {
		t.Fatal("downgrade removed original policy enforcement", err)
	}
	if _, err := pool.Exec(t.Context(), up); err != nil {
		t.Fatal("upgrade", err)
	}
	var empty bool
	if err := pool.QueryRow(t.Context(), "SELECT environment_binding_qualification_deadline('00000000-0000-0000-0000-000000000001'::uuid) IS NULL").Scan(&empty); err != nil || !empty {
		t.Fatal("nonexistent graph qualified", err)
	}
}
