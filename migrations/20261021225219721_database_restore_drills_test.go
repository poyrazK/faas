//go:build !no_pg

package migrations_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// A drill's result and finish agree, and its error code is a plain code.
func TestMigration_DatabaseRestoreDrills(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	migrateUpOnce(ctx, t, pool)
	insert := func(result string, finished bool, code *string) error {
		_, err := pool.Exec(ctx, `INSERT INTO database_restore_drills (drill_id, node_id, source_id, target_at, result, finished_at, error_code)
			VALUES ($1, 'db-a', $2, now(), $3, CASE WHEN $4 THEN now() END, $5)`, uuid.New(), uuid.New(), result, finished, code)
		return err
	}
	code := func(s string) *string { return &s }
	for name, err := range map[string]error{
		"running":            insert("running", false, nil),
		"ok":                 insert("ok", true, nil),
		"failed with a code": insert("failed", true, code("restore_failed")),
		"skipped":            insert("skipped", true, code("drill_no_capacity")),
	} {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"unknown result":       insert("maybe", true, nil),
		"running but finished": insert("running", true, nil),
		"ok but unfinished":    insert("ok", false, nil),
		"bad error code":       insert("failed", true, code("Bad Code")),
		"empty error code":     insert("failed", true, code("")),
	} {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
