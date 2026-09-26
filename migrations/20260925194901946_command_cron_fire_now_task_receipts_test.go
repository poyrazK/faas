//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_CommandCronFireNowTaskReceipt(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var nullable bool
	if err := pool.QueryRow(ctx, `
		select not attnotnull
		  from pg_attribute a
		  join pg_class t on t.oid = a.attrelid
		 where t.relname = 'cron_fire_now_requests'
		   and a.attname = 'task_id' and not a.attisdropped`).Scan(&nullable); err != nil {
		t.Fatalf("read task_id column: %v", err)
	}
	if !nullable {
		t.Fatal("cron_fire_now_requests.task_id must be nullable")
	}

	var deleteAction string
	if err := pool.QueryRow(ctx, `
		select c.confdeltype::text
		  from pg_constraint c
		  join pg_class t on t.oid = c.conrelid
		  join pg_class r on r.oid = c.confrelid
		 where t.relname = 'cron_fire_now_requests'
		   and r.relname = 'app_tasks' and c.contype = 'f'
		   and pg_get_constraintdef(c.oid) like '%(task_id)%'`).Scan(&deleteAction); err != nil {
		t.Fatalf("read task_id foreign key: %v", err)
	}
	if deleteAction != "n" {
		t.Fatalf("task_id foreign key delete action = %q, want SET NULL ('n')", deleteAction)
	}
}
