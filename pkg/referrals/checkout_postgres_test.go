package referrals_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/referrals"
)

func checkoutFixture(t *testing.T) (*pgxpool.Pool, *referrals.PostgresStore, referrals.Link, string) {
	t.Helper()
	pool, store, owner, account := fixture(t)
	_, err := pool.Exec(t.Context(), `ALTER TABLE accounts ADD COLUMN mfa_enrolled_at timestamptz DEFAULT now(),
 ADD COLUMN stripe_subscription_item text, ADD COLUMN created_at timestamptz DEFAULT now();`)
	if err != nil {
		t.Fatal(err)
	}
	link, err := store.EnsureLink(t.Context(), owner, "sales")
	if err != nil {
		t.Fatal(err)
	}
	return pool, store, link, account
}

func checkoutTerms(code string) referrals.Attribution {
	return referrals.Attribution{Code: code, Policy: referrals.Policy{
		Currency: "usd", CommissionBPS: 1250, HoldDays: 0, ConversionDays: 90,
	}}
}

func TestCheckoutReferralFirstPurchaseAndImmutableTerms(t *testing.T) {
	pool, store, link, account := checkoutFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, "UPDATE accounts SET created_at=now()-interval '2 years' WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	status, err := store.CheckoutState(ctx, account)
	if err != nil || !status.Eligible {
		t.Fatal(status, err)
	}
	created, err := store.AttributeCheckout(ctx, account, checkoutTerms(link.Code), []string{"rep@example.com"})
	if err != nil || !created {
		t.Fatal(created, err)
	}
	retry := checkoutTerms(link.Code)
	retry.Policy.Currency, retry.Policy.CommissionBPS = "eur", 9000
	created, err = store.AttributeCheckout(ctx, account, retry, []string{"rep@example.com"})
	if err != nil || created {
		t.Fatal("retry rewrote attribution", created, err)
	}
	status, err = store.CheckoutState(ctx, account)
	if err != nil || status.Eligible || status.Reason != "already_attributed" || status.Code != link.Code {
		t.Fatal(status, err)
	}
	invoice := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents)
 VALUES ($1,$2,'paid','usd',1200,1000,200)`, invoice, account); err != nil {
		t.Fatal(err)
	}
	if err = referrals.RecordPayment(ctx, pool, invoice); err != nil {
		t.Fatal(err)
	}
	rows, err := store.List(ctx, referrals.Scope{OwnerID: link.OwnerID}, "", 25)
	if err != nil || len(rows) != 1 || !rows[0].Paid || rows[0].RewardCents != 125 ||
		rows[0].Currency != "usd" || rows[0].AttributionSource != "checkout" {
		t.Fatalf("checkout payment attribution: %+v %v", rows, err)
	}
}

func TestCheckoutReferralEligibilityAndCodeGuards(t *testing.T) {
	tests := []struct {
		name    string
		setup   string
		members []string
		want    error
	}{
		{"previous payment", `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents) VALUES (gen_random_uuid(),$1,'paid','usd',100,100,0)`, []string{"rep@example.com"}, referrals.ErrCheckoutIneligible},
		{"refunded purchase", `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents,amount_refunded_cents) VALUES (gen_random_uuid(),$1,'void','usd',0,100,0,100)`, []string{"rep@example.com"}, referrals.ErrCheckoutIneligible},
		{"paid plan", `UPDATE accounts SET plan='pro' WHERE id=$1`, []string{"rep@example.com"}, referrals.ErrCheckoutIneligible},
		{"subscription", `UPDATE accounts SET stripe_subscription_item='subscription' WHERE id=$1`, []string{"rep@example.com"}, referrals.ErrCheckoutIneligible},
		{"suspended buyer", `UPDATE accounts SET status='suspended' WHERE id=$1`, []string{"rep@example.com"}, referrals.ErrCheckoutIneligible},
		{"revoked rep", "", nil, referrals.ErrInvalidCheckoutCode},
		{"paused code", `UPDATE referral_links SET active=false WHERE owner_id<>$1`, []string{"rep@example.com"}, referrals.ErrInvalidCheckoutCode},
		{"unverified rep", `UPDATE accounts SET email_verified_at=NULL WHERE id<>$1`, []string{"rep@example.com"}, referrals.ErrInvalidCheckoutCode},
		{"unenrolled rep", `UPDATE accounts SET mfa_enrolled_at=NULL WHERE id<>$1`, []string{"rep@example.com"}, referrals.ErrInvalidCheckoutCode},
		{"customer code", `UPDATE referral_links SET kind='customer' WHERE owner_id<>$1`, []string{"rep@example.com"}, referrals.ErrInvalidCheckoutCode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, store, link, account := checkoutFixture(t)
			if tt.setup != "" {
				if _, err := pool.Exec(t.Context(), tt.setup, account); err != nil {
					t.Fatal(err)
				}
			}
			created, err := store.AttributeCheckout(t.Context(), account, checkoutTerms(link.Code), tt.members)
			if created || !errors.Is(err, tt.want) {
				t.Fatal(created, err)
			}
			var count int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM referral_attributions WHERE account_id=$1", account).Scan(&count); err != nil || count != 0 {
				t.Fatal("refused choice was persisted", count, err)
			}
		})
	}
}

func TestCheckoutReferralPreservesSignupAndRejectsSelf(t *testing.T) {
	pool, store, link, account := checkoutFixture(t)
	ctx := t.Context()
	if created, err := store.AttributeCheckout(ctx, link.OwnerID, checkoutTerms(link.Code), []string{"rep@example.com"}); created || !errors.Is(err, referrals.ErrInvalidCheckoutCode) {
		t.Fatal("self referral", created, err)
	}
	customerCode, err := store.EnsureLink(ctx, link.OwnerID, "customer")
	if err != nil {
		t.Fatal(err)
	}
	if err := referrals.Attribute(ctx, pool, account, checkoutTerms(customerCode.Code)); err != nil {
		t.Fatal(err)
	}
	if created, err := store.AttributeCheckout(ctx, account, checkoutTerms(link.Code), []string{"rep@example.com"}); created || !errors.Is(err, referrals.ErrAlreadyAttributed) {
		t.Fatal("replaced signup referral", created, err)
	}
}

func TestCheckoutReferralConcurrentChoicesHaveOneWinner(t *testing.T) {
	pool, store, first, _ := checkoutFixture(t)
	secondOwner, account := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(t.Context(), "INSERT INTO accounts(id,email) VALUES ($1,'second-rep@example.com'),($2,'buyer@example.com')", secondOwner, account); err != nil {
		t.Fatal(err)
	}
	second, err := store.EnsureLink(t.Context(), secondOwner, "sales")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type result struct {
		created bool
		err     error
	}
	results := make(chan result, 2)
	for _, code := range []string{first.Code, second.Code} {
		go func() {
			<-start
			c, e := store.AttributeCheckout(t.Context(), account, checkoutTerms(code), []string{"rep@example.com", "second-rep@example.com"})
			results <- result{c, e}
		}()
	}
	close(start)
	created, conflicts := 0, 0
	for range 2 {
		r := <-results
		switch {
		case r.err == nil && r.created:
			created++
		case errors.Is(r.err, referrals.ErrAlreadyAttributed):
			conflicts++
		default:
			t.Fatal(r)
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatal("competing codes were not serialized", created, conflicts)
	}
}

func TestCheckoutReferralWaitsForConcurrentPayment(t *testing.T) {
	pool, store, link, account := checkoutFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := referrals.LockBillingAccount(ctx, tx, account); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents) VALUES (gen_random_uuid(),$1,'paid','usd',100,100,0)`, account); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := store.AttributeCheckout(ctx, account, checkoutTerms(link.Code), []string{"rep@example.com"})
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("selection did not wait for payment: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, referrals.ErrCheckoutIneligible) {
		t.Fatal(err)
	}
}

func TestCheckoutReferralNeverCreditsAnOlderInvoiceOrLaterRenewal(t *testing.T) {
	pool, store, link, account := checkoutFixture(t)
	ctx := t.Context()
	old, newer := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents) VALUES ($1,$2,'open','usd',0,1000,0)`, old, account); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttributeCheckout(ctx, account, checkoutTerms(link.Code), []string{"rep@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE invoices SET status='paid',amount_paid_cents=1000 WHERE id=$1", old); err != nil {
		t.Fatal(err)
	}
	if err := referrals.RecordPayment(ctx, pool, old); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO invoices(id,account_id,status,currency,amount_paid_cents,subtotal_cents,tax_cents) VALUES ($1,$2,'paid','usd',1000,1000,0)`, newer, account); err != nil {
		t.Fatal(err)
	}
	if err := referrals.RecordPayment(ctx, pool, newer); err != nil {
		t.Fatal(err)
	}
	rows, err := store.List(ctx, referrals.Scope{OwnerID: link.OwnerID}, "", 25)
	if err != nil || len(rows) != 1 || rows[0].Paid || rows[0].RewardStatus != "registered" {
		t.Fatalf("retroactive conversion: %+v %v", rows, err)
	}
}
