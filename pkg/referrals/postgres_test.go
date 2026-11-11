package referrals_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/referrals"
)

func fixture(t *testing.T) (*pgxpool.Pool, *referrals.PostgresStore, string, string) {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `CREATE TABLE accounts (id uuid PRIMARY KEY,email text NOT NULL,plan text NOT NULL DEFAULT 'free',status text NOT NULL DEFAULT 'active',email_verified_at timestamptz DEFAULT now());
CREATE TABLE apps(id uuid PRIMARY KEY,account_id uuid NOT NULL);
CREATE TABLE invoices(id uuid PRIMARY KEY,account_id uuid NOT NULL,status text NOT NULL,currency text NOT NULL,amount_paid_cents bigint NOT NULL,subtotal_cents bigint NOT NULL,tax_cents bigint NOT NULL,amount_refunded_cents bigint NOT NULL DEFAULT 0,amount_refund_pending_cents bigint NOT NULL DEFAULT 0,created_at timestamptz NOT NULL DEFAULT now());`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261111125844832_sales_referrals.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err = pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	raw, err = migrations.FS.ReadFile("20261111125845906_short_referral_codes.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ = strings.Cut(string(raw), "-- +goose Down")
	if _, err = pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	raw, err = migrations.FS.ReadFile("20261111125846935_validate_referral_codes.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ = strings.Cut(string(raw), "-- +goose Down")
	// NO TRANSACTION migrations commit each statement independently.
	for _, statement := range strings.Split(up, ";") {
		if strings.TrimSpace(statement) != "" {
			if _, err = pool.Exec(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	owner, other := uuid.NewString(), uuid.NewString()
	if _, err = pool.Exec(ctx, "INSERT INTO accounts(id,email) VALUES ($1,'rep@example.com'),($2,'other@example.com')", owner, other); err != nil {
		t.Fatal(err)
	}
	return pool, referrals.NewPostgresStore(pool), owner, other
}

func attribute(t *testing.T, pool *pgxpool.Pool, link referrals.Link, policy referrals.Policy) string {
	t.Helper()
	id := uuid.NewString()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "INSERT INTO accounts(id,email) VALUES ($1,$2)", id, id+"@example.com"); err != nil {
		t.Fatal(err)
	}
	if err = referrals.Attribute(ctx, tx, id, referrals.Attribution{Code: link.Code, Policy: policy}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAttributionOwnershipAndRetry(t *testing.T) {
	pool, store, owner, other := fixture(t)
	ctx := t.Context()
	link, err := store.EnsureLink(ctx, owner, "sales")
	if err != nil {
		t.Fatal(err)
	}
	same, err := store.EnsureLink(ctx, owner, "sales")
	if err != nil || link.Code != same.Code {
		t.Fatal("link retries must return the same link", err)
	}
	policy := referrals.Policy{Currency: "usd", CommissionBPS: 1250, HoldDays: 30, ConversionDays: 90}
	id := attribute(t, pool, link, policy)
	customerRows, err := store.List(ctx, referrals.Scope{OwnerID: owner, Kind: "customer"}, "", 25)
	if err != nil || len(customerRows) != 0 {
		t.Fatal("sales data escaped into customer program", customerRows, err)
	}
	foreign, err := store.EnsureLink(ctx, other, "sales")
	if err != nil {
		t.Fatal(err)
	}
	if err = referrals.Attribute(ctx, pool, id, referrals.Attribution{Code: foreign.Code, Policy: policy}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.List(ctx, referrals.Scope{OwnerID: owner}, "", 25)
	if err != nil || len(rows) != 1 || rows[0].AccountID != id {
		t.Fatalf("lost first attribution: %+v %v", rows, err)
	}
	rows, err = store.List(ctx, referrals.Scope{OwnerID: other}, "", 25)
	if err != nil || len(rows) != 0 {
		t.Fatalf("foreign read escaped scope: %+v %v", rows, err)
	}
	changed, err := store.UpdateLead(ctx, referrals.Scope{OwnerID: other}, other, id, referrals.LeadUpdate{Stage: "qualified"})
	if err != nil || changed {
		t.Fatalf("foreign update escaped scope: %v %v", changed, err)
	}
	changed, err = store.UpdateLead(ctx, referrals.Scope{OwnerID: other, All: true}, other, id, referrals.LeadUpdate{Stage: "contacted", Note: "Book a call", FollowUp: "2026-12-01"})
	if err != nil || !changed {
		t.Fatalf("manager update failed: %v %v", changed, err)
	}
	rows, err = store.List(ctx, referrals.Scope{OwnerID: owner}, "", 25)
	if err != nil || rows[0].Note != "Book a call" || rows[0].FollowUp != "2026-12-01" {
		t.Fatal(rows, err)
	}
	if err = referrals.Attribute(ctx, pool, owner, referrals.Attribution{Code: link.Code, Policy: policy}); err != nil {
		t.Fatal(err)
	}
	summary, err := store.Summary(ctx, referrals.Scope{OwnerID: owner})
	if err != nil || len(summary) != 1 || summary[0].Signups != 1 {
		t.Fatal("self referral counted", summary, err)
	}
}

func TestRewardsFollowPaidInvoiceAndRefunds(t *testing.T) {
	pool, store, owner, _ := fixture(t)
	ctx := t.Context()
	link, err := store.EnsureLink(ctx, owner, "sales")
	if err != nil {
		t.Fatal(err)
	}
	id := attribute(t, pool, link, referrals.Policy{Currency: "usd", CommissionBPS: 1250, HoldDays: 0, ConversionDays: 90})
	assert := func(status string, paid bool, reward int64) {
		t.Helper()
		rows, err := store.List(ctx, referrals.Scope{OwnerID: owner}, "", 25)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		r := rows[0]
		if r.RewardStatus != status || r.Paid != paid || r.RewardCents != reward {
			t.Fatalf("got %+v, want %s/%v/%d", r, status, paid, reward)
		}
	}
	assert("registered", false, 0)
	invoice := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents) VALUES ($1,$2,'open','usd',0,1003,200)`, invoice, id); err != nil {
		t.Fatal(err)
	}
	assert("registered", false, 0)
	if _, err = pool.Exec(ctx, "UPDATE invoices SET status='paid',amount_paid_cents=1203 WHERE id=$1", invoice); err != nil {
		t.Fatal(err)
	}
	if err = referrals.RecordPayment(ctx, pool, invoice); err != nil {
		t.Fatal(err)
	}
	assert("eligible", true, 125) // fractions of a cent round down
	if _, err = pool.Exec(ctx, "UPDATE invoices SET amount_refund_pending_cents=1 WHERE id=$1", invoice); err != nil {
		t.Fatal(err)
	}
	assert("reversed", true, 125)
	if _, err = pool.Exec(ctx, `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents,created_at) VALUES ($1,$2,'paid','usd',99999,99999,0,now()+interval '1 minute')`, uuid.NewString(), id); err != nil {
		t.Fatal(err)
	}
	assert("reversed", true, 125) // a second payment cannot replace refunded evidence
	summary, err := store.Summary(ctx, referrals.Scope{OwnerID: owner})
	if err != nil || summary[0].Eligible != 0 || summary[0].RewardCents != 0 {
		t.Fatal(summary, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE invoices SET amount_refund_pending_cents=0,currency='eur' WHERE id=$1", invoice); err != nil {
		t.Fatal(err)
	}
	assert("currency_mismatch", true, 125)
}

func TestCustomerTermsHoldPauseAndAtomicRollback(t *testing.T) {
	pool, store, owner, _ := fixture(t)
	ctx := t.Context()
	link, err := store.EnsureLink(ctx, owner, "customer")
	if err != nil {
		t.Fatal(err)
	}
	policy := referrals.Policy{Currency: "usd", CustomerRewardCents: 2500, ReferredRewardCents: 1000, HoldDays: 30, ConversionDays: 90}
	id := attribute(t, pool, link, policy)
	if _, err = pool.Exec(ctx, `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents) VALUES ($1,$2,'paid','usd',5000,5000,0)`, uuid.NewString(), id); err != nil {
		t.Fatal(err)
	}
	var paidInvoice string
	if err = pool.QueryRow(ctx, "SELECT id FROM invoices WHERE account_id=$1", id).Scan(&paidInvoice); err != nil {
		t.Fatal(err)
	}
	if err = referrals.RecordPayment(ctx, pool, paidInvoice); err != nil {
		t.Fatal(err)
	}
	rows, err := store.List(ctx, referrals.Scope{OwnerID: owner}, "", 25)
	if err != nil || rows[0].RewardStatus != "holding" || rows[0].RewardCents != 2500 || rows[0].ReferredRewardCents != 1000 {
		t.Fatal(rows, err)
	}
	if _, err = store.SetLinkActive(ctx, referrals.Scope{OwnerID: owner}, link.Code, false); err != nil {
		t.Fatal(err)
	}
	_ = attribute(t, pool, link, policy)
	rows, err = store.List(ctx, referrals.Scope{OwnerID: owner}, "", 25)
	if err != nil || len(rows) != 1 {
		t.Fatal("paused link attributed", rows, err)
	}
	if _, err = store.SetLinkActive(ctx, referrals.Scope{OwnerID: owner}, link.Code, true); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolled := uuid.NewString()
	if _, err = tx.Exec(ctx, "INSERT INTO accounts(id,email) VALUES ($1,'rolled@example.com')", rolled); err != nil {
		t.Fatal(err)
	}
	if err = referrals.Attribute(ctx, tx, rolled, referrals.Attribution{Code: link.Code, Policy: policy}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	rows, err = store.List(ctx, referrals.Scope{OwnerID: owner}, "", 25)
	if err != nil || len(rows) != 1 {
		t.Fatal("rolled-back signup left attribution", rows, err)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM accounts WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	rows, err = store.List(ctx, referrals.Scope{OwnerID: owner}, "", 25)
	if err != nil || len(rows) != 0 {
		t.Fatal("deleted account retained attribution", rows, err)
	}
}

func TestTouchTokens(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	now := time.Now().Truncate(time.Second)
	code := referrals.NewCode()
	token := referrals.SignTouch(key, code, now)
	if referrals.VerifyTouch(key, token, now, time.Hour) != code {
		t.Fatal("valid touch rejected")
	}
	for _, tc := range []struct {
		name, value string
		at          time.Time
		key         []byte
	}{{"tampered", token + "x", now, key}, {"expired", token, now.Add(time.Hour), key}, {"future", token, now.Add(-time.Second), key}, {"other key", token, now, []byte(strings.Repeat("x", 32))}, {"empty key", token, now, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			if referrals.VerifyTouch(tc.key, tc.value, tc.at, time.Hour) != "" {
				t.Fatal("invalid touch accepted")
			}
		})
	}
}
