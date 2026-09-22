//go:build !no_pg

// adr: 213

package meter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestLogEventRetentionUsesAccountPlanAndOneDayOrphanFloor(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	type eventCase struct {
		plan string
		age  time.Duration
		keep bool
	}
	cases := []eventCase{
		{"free", 48 * time.Hour, false},
		{"hobby", 48 * time.Hour, true},
		{"hobby", 8 * 24 * time.Hour, false},
		{"pro", 20 * 24 * time.Hour, true},
		{"pro", 31 * 24 * time.Hour, false},
		{"scale", 80 * 24 * time.Hour, true},
		{"scale", 91 * 24 * time.Hour, false},
		{"", 48 * time.Hour, false},
	}
	ids := make([]string, len(cases))
	for i, tc := range cases {
		accountID := uuid.NewString()
		if tc.plan != "" {
			if _, err := pool.Exec(ctx,
				"insert into accounts (id, email, plan) values ($1, $2, $3)",
				accountID, fmt.Sprintf("log-retention-%d@example.test", i), tc.plan,
			); err != nil {
				t.Fatal(err)
			}
		}
		ids[i] = uuid.NewString()
		if _, err := pool.Exec(ctx, `
			insert into log_events (id, occurred_at, account_id, app_id, source, message)
			values ($1, $2, $3, $4, 'http', 'retention check')`,
			ids[i], now.Add(-tc.age), accountID, uuid.NewString(),
		); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := RetentionOnceLogEvents(ctx, partitionTestDB{pool: pool})
	if err != nil || deleted != 5 {
		t.Fatalf("deleted=%d, err=%v, want 5", deleted, err)
	}
	for i, tc := range cases {
		var exists bool
		if err := pool.QueryRow(ctx, "select exists(select 1 from log_events where id=$1)", ids[i]).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != tc.keep {
			t.Fatalf("case %d (%s, age %s): exists=%v, want %v", i, tc.plan, tc.age, exists, tc.keep)
		}
	}
	if again, err := RetentionOnceLogEvents(ctx, partitionTestDB{pool: pool}); err != nil || again != 0 {
		t.Fatalf("second sweep deleted=%d, err=%v", again, err)
	}
}

func TestEnsureLogEventPartitionsMovesDefaultRowsAndDropsOldMonths(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var schemaName, currentName string
	if err := pool.QueryRow(ctx, `
		select current_schema(), 'log_events_' || to_char(now() AT TIME ZONE 'UTC', 'YYYYMM')`,
	).Scan(&schemaName, &currentName); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "drop table "+pgx.Identifier{schemaName, currentName}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into log_events (id, occurred_at, account_id, app_id, source, message)
		values ($1, now(), $2, $3, 'http', 'partition check')`,
		id, uuid.NewString(), uuid.NewString(),
	); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRow(ctx, "select tableoid::regclass::text from log_events where id=$1", id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != "log_events_default" {
		t.Fatalf("initial partition=%q, want default", before)
	}
	for pass := 0; pass < 2; pass++ {
		coverage, err := EnsureLogEventPartitions(ctx, partitionTestDB{pool: pool})
		if err != nil || coverage.CoveredMonths != 3 || !coverage.CurrentMonthCovered || coverage.DefaultRows != 0 {
			t.Fatalf("pass %d: coverage=%+v, err=%v", pass, coverage, err)
		}
	}
	var after string
	if err := pool.QueryRow(ctx, "select tableoid::regclass::text from log_events where id=$1", id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != currentName {
		t.Fatalf("reconciled partition=%q, want %q", after, currentName)
	}
	oldStart := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, -6, 0)
	oldStart = time.Date(oldStart.Year(), oldStart.Month(), 1, 0, 0, 0, 0, time.UTC)
	oldEnd := oldStart.AddDate(0, 1, 0)
	oldName := "log_events_" + oldStart.Format("200601")
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		"create table %s partition of %s for values from ('%s') to ('%s')",
		pgx.Identifier{schemaName, oldName}.Sanitize(),
		pgx.Identifier{schemaName, "log_events"}.Sanitize(),
		oldStart.Format(time.RFC3339), oldEnd.Format(time.RFC3339),
	)); err != nil {
		t.Fatal(err)
	}
	if err := DropExpiredLogEventPartitions(ctx, partitionTestDB{pool: pool}); err != nil {
		t.Fatal(err)
	}
	var oldExists bool
	if err := pool.QueryRow(ctx, "select to_regclass($1) is not null", schemaName+"."+oldName).Scan(&oldExists); err != nil {
		t.Fatal(err)
	}
	if oldExists {
		t.Fatalf("expired partition %s remains attached", oldName)
	}
}
