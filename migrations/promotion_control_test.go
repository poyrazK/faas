//go:build !no_pg

package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPromotionControlMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	files, err := filepath.Glob("*_promotion_controls.sql")
	if err != nil || len(files) != 1 {
		t.Fatalf("promotion control migration: %v %v", files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	if _, err = pool.Exec(t.Context(), down); err != nil {
		t.Fatal("empty downgrade", err)
	}
	if _, err = pool.Exec(t.Context(), up); err != nil {
		t.Fatal("reinstall", err)
	}
	var table string
	if err = pool.QueryRow(t.Context(), "SELECT 'project_environment_promotion_controls'::regclass::text").Scan(&table); err != nil {
		t.Fatal(err)
	}
}
