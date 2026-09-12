//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationJobTaskCanonicalExitClasses(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)

	var definition string
	if err := pool.QueryRow(t.Context(), `
		select pg_get_constraintdef(oid)
		  from pg_constraint
		 where conrelid = 'job_tasks'::regclass
		   and conname = 'job_tasks_error_class_check'
	`).Scan(&definition); err != nil {
		t.Fatalf("load job_tasks_error_class_check: %v", err)
	}
	for _, value := range []string{"'succeeded'", "'failed'"} {
		if !strings.Contains(definition, value) {
			t.Fatalf("job_tasks_error_class_check missing %s: %s", value, definition)
		}
	}
}
