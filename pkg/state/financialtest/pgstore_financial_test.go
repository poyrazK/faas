package financialtest

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
)

// adr: 566 — financial history must survive retention and transaction failure.
func TestFinancialPostgresTransactions(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	a := financialAccount(t, store)
	start, end := financialPeriod()
	minute := start.Add(time.Hour)
	app, instance := uuid.NewString(), uuid.NewString()
	insert := `insert into usage_minutes(account_id, app_id, instance_id, minute, mb_seconds, requests) values($1,$2,$3,$4,100,0)`

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, insert, a.ID, app, instance, minute); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	head, err := store.FinancialEvidenceHead(ctx, a.ID, start, end)
	if err != nil || head != 0 {
		t.Fatalf("rolled back usage leaked evidence: %d, %v", head, err)
	}
	if err := store.AppendUsage(ctx, a.ID, app, instance, minute, 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	head, err = store.FinancialEvidenceHead(ctx, a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	before := financialRows(t, store, a.ID, head)
	if _, err := pool.Exec(ctx, `update usage_minutes set mb_seconds=99 where instance_id=$1`, instance); err == nil {
		t.Fatal("correction rewrote historical usage")
	}
	var quantity int64
	if err := pool.QueryRow(ctx, `select mb_seconds from usage_minutes where instance_id=$1`, instance).Scan(&quantity); err != nil || quantity != 100 {
		t.Fatalf("failed correction did not roll back: %d, %v", quantity, err)
	}
	if _, err := pool.Exec(ctx, `delete from usage_minutes where instance_id=$1`, instance); err != nil {
		t.Fatal(err)
	}
	if got := financialRows(t, store, a.ID, head); !reflect.DeepEqual(got, before) {
		t.Fatalf("retention deleted financial history: %+v", got)
	}
}

// adr: 566 — trigger upgrades and rollback preserve already retained evidence.
func TestFinancialPostgresIntervalPlanMigrationReplay(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	a := financialAccount(t, store)
	start, end := financialPeriod()
	if err := store.AppendUsage(ctx, a.ID, uuid.NewString(), uuid.NewString(), start, 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	head, err := store.FinancialEvidenceHead(ctx, a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	before := financialRows(t, store, a.ID, head)
	data, err := migrations.FS.ReadFile("20261004094329557_financial_interval_plan_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.Split(string(data), "-- +goose Down")
	if len(sections) != 2 {
		t.Fatal("migration must contain both forward and rollback statements")
	}
	for _, statements := range []string{sections[0], sections[1], sections[0], sections[0]} {
		if _, err := pool.Exec(ctx, statements); err != nil {
			t.Fatalf("trigger migration replay: %v", err)
		}
		if got := financialRows(t, store, a.ID, head); !reflect.DeepEqual(got, before) {
			t.Fatalf("migration changed retained evidence: %+v", got)
		}
	}
	financialIntervalPlanSuite(t, store)
}

// adr: 566 — a fixed read head excludes transactions committed later.
func TestFinancialPostgresCommitOrder(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	a := financialAccount(t, store)
	start, end := financialPeriod()
	insert := `insert into usage_minutes(account_id, app_id, instance_id, minute, mb_seconds, requests) values($1,$2,$3,$4,100,0)`
	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	if _, err := first.Exec(ctx, insert, a.ID, uuid.NewString(), uuid.NewString(), start); err != nil {
		t.Fatal(err)
	}
	second, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Rollback(ctx) }()
	done := make(chan error, 1)
	go func() {
		_, err := second.Exec(ctx, insert, a.ID, uuid.NewString(), uuid.NewString(), start.Add(time.Minute))
		done <- err
	}()
	// Observe the lock instead of depending on goroutine scheduling or sleeps.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	waiting := false
	for !waiting {
		select {
		case err := <-done:
			t.Fatalf("evidence allocated before account commit: %v", err)
		case <-timeout.C:
			t.Fatal("second writer did not reach the account evidence lock")
		case <-ticker.C:
			if err := pool.QueryRow(ctx, `select exists(select 1 from pg_locks where locktype='advisory' and not granted)`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	head, err := store.FinancialEvidenceHead(ctx, a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	before := financialRows(t, store, a.ID, head)
	if len(before) != 1 {
		t.Fatalf("uncommitted evidence visible: %+v", before)
	}
	if err := second.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := financialRows(t, store, a.ID, head); !reflect.DeepEqual(got, before) {
		t.Fatalf("late commit changed snapshot: %+v", got)
	}
	latest, err := store.FinancialEvidenceHead(ctx, a.ID, start, end)
	if err != nil || latest <= head || len(financialRows(t, store, a.ID, latest)) != 2 {
		t.Fatalf("new snapshot lost second commit: %d, %v", latest, err)
	}
}
