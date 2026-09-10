//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_JobsAndCronContractRepair(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var jobsCheck, cronDelete string
	if err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(oid)
		  from pg_constraint
		 where conrelid = 'jobs'::regclass and conname = 'jobs_kind_check'
	`).Scan(&jobsCheck); err != nil {
		t.Fatalf("read jobs kind constraint: %v", err)
	}
	if !strings.Contains(jobsCheck, "'batch'::text") ||
		!strings.Contains(jobsCheck, "'recurring'::text") ||
		strings.Contains(jobsCheck, "'app'::text") ||
		strings.Contains(jobsCheck, "'function'::text") {
		t.Fatalf("jobs_kind_check = %q", jobsCheck)
	}

	if err := pool.QueryRow(ctx, `
		select confdeltype::text
		  from pg_constraint
		 where conrelid = 'invocations'::regclass
		   and conname = 'invocations_cron_id_fkey'
	`).Scan(&cronDelete); err != nil {
		t.Fatalf("read invocation cron foreign key: %v", err)
	}
	if cronDelete != "n" {
		t.Fatalf("invocations cron delete action = %q, want SET NULL (n)", cronDelete)
	}
}
