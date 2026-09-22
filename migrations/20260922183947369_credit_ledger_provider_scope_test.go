//go:build !no_pg

// spec: §10 — provider-qualified invoice retries cannot double-debit credits.
// spec: §11 — backfills must preserve account ownership of billing evidence.
package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestCreditLedgerProviderMigration(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20260922164807594)
	type fixture struct {
		name, account, credit, wantProvider string
		providers                           []string
	}
	cases := []fixture{
		{name: "stripe", providers: []string{"stripe"}, wantProvider: "stripe"},
		{name: "polar", providers: []string{"polar"}, wantProvider: "polar"},
		{name: "ambiguous", providers: []string{"stripe", "polar"}},
		{name: "missing"},
	}
	for i := range cases {
		tc := &cases[i]
		tc.account, tc.credit = uuid.NewString(), uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,plan) VALUES ($1,$2,'hobby')`, tc.account, tc.name+"@example.com"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO account_credits (id,account_id,cents_remaining,reason) VALUES ($1,$2,200,'test credit')`, tc.credit, tc.account); err != nil {
			t.Fatal(err)
		}
		for _, provider := range tc.providers {
			if _, err := pool.Exec(ctx, `INSERT INTO invoices (account_id,provider,provider_invoice_id,plan,status,period_start,period_end,total_cents,amount_paid_cents) VALUES ($1,$2,'shared','hobby','paid','2026-09-01','2026-10-01',200,200)`, tc.account, provider); err != nil {
				t.Fatal(err)
			}
		}
		// Preserve both sides of a fully compensated legacy debit, and an
		// issuance row that must remain independent of provider invoices.
		if _, err := pool.Exec(ctx, `INSERT INTO credit_ledger (account_id,credit_id,delta_cents,reason,actor,provider_invoice_id) VALUES ($1,$2,-60,'consumption','test','shared'),($1,$2,60,'compensation','test','shared'),($1,$2,200,'issuance','test',NULL)`, tc.account, tc.credit); err != nil {
			t.Fatal(err)
		}
	}
	migrateUpTo(t, ctx, pool, 20260922183947369)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
	// Evidence added after cutover cannot retroactively identify an old debit.
	// Simulate a schema-ahead-of-goose replay and keep that ledger row unresolved.
	if _, err := pool.Exec(ctx, `INSERT INTO invoices (account_id,provider,provider_invoice_id,plan,status,period_start,period_end,total_cents,amount_paid_cents) VALUES ($1,'polar','shared','hobby','paid','2026-09-01','2026-10-01',200,200)`, cases[3].account); err != nil {
		t.Fatal(err)
	}
	tag, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=20260922183947369`)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("remove provider migration ledger row = (%d, %v), want one row", tag.RowsAffected(), err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("provider migration schema replay: %v", err)
	}
	for _, tc := range cases {
		var matched, issuance int
		var balance, netDebit int64
		if err := pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(delta_cents),0) FROM credit_ledger WHERE account_id=$1 AND provider_invoice_id='shared' AND provider=$2`, tc.account, tc.wantProvider).Scan(&matched, &netDebit); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_ledger WHERE account_id=$1 AND provider_invoice_id IS NULL AND provider=''`, tc.account).Scan(&issuance); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT cents_remaining FROM account_credits WHERE id=$1`, tc.credit).Scan(&balance); err != nil {
			t.Fatal(err)
		}
		if matched != 2 || netDebit != 0 || issuance != 1 || balance != 200 {
			t.Errorf("%s: matched=%d net=%d issuance=%d balance=%d", tc.name, matched, netDebit, issuance, balance)
		}
	}
	// One credit can fund the same invoice ID at different providers, but
	// a provider-qualified replay still has a durable uniqueness backstop.
	insert := `INSERT INTO credit_ledger (account_id,credit_id,delta_cents,reason,actor,provider,provider_invoice_id) VALUES ($1,$2,-20,'consumption','test',$3,'shared')`
	for _, tc := range []struct{ provider, code string }{{"stripe", "23505"}, {"polar", ""}, {"polar", "23505"}, {"unknown", "23514"}} {
		_, err := pool.Exec(ctx, insert, cases[0].account, cases[0].credit, tc.provider)
		if tc.code == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
			t.Fatalf("%s insert = %v, want SQLSTATE %s", tc.provider, err, tc.code)
		}
	}
	// Rollback cannot collapse distinct providers by erasing ledger evidence.
	source, err := migrations.FS.ReadFile("20260922183947369_credit_ledger_provider_scope.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, down)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("unsafe rollback = %v, want uniqueness failure", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_ledger WHERE credit_id=$1 AND provider='polar'`, cases[0].credit).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback changed provider evidence: count=%d err=%v", count, err)
	}
}
