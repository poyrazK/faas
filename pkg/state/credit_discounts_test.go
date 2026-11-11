package state_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104212028 — each credit discount apid attached is kept with its
// state; an order redeems it once, and subjects with credit or an attached
// discount are listed for the sync pass.
func exerciseCreditDiscounts(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "credit-discount-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "credit-discount-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	discountA, discountB := "disc-"+uuid.NewString(), "disc-"+uuid.NewString()
	first, err := store.CreateCreditDiscount(ctx, state.CreditDiscount{SubjectID: acct.ID, Provider: "polar",
		ProviderSubscriptionID: "sub-1", ProviderDiscountID: discountA, AmountCents: 500, Currency: "usd"})
	if err != nil || first.ID == "" || first.State != state.CreditDiscountAttached {
		t.Fatalf("create = %+v, %v", first, err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := store.CreateCreditDiscount(ctx, state.CreditDiscount{SubjectID: acct.ID, Provider: "polar",
		ProviderSubscriptionID: "sub-1", ProviderDiscountID: discountB, AmountCents: 800, Currency: "usd"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCreditDiscount(ctx, state.CreditDiscount{SubjectID: acct.ID, Provider: "polar",
		ProviderSubscriptionID: "sub-1", ProviderDiscountID: discountA, AmountCents: 1, Currency: "usd"}); err == nil {
		t.Fatal("a provider discount was recorded twice")
	}

	attached, err := store.AttachedCreditDiscounts(ctx, acct.ID, "polar")
	if err != nil || len(attached) != 2 || attached[0].ID != second.ID {
		t.Fatalf("attached = %+v, %v; want both, newest first", attached, err)
	}
	if err := store.DetachCreditDiscount(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if attached, _ := store.AttachedCreditDiscounts(ctx, acct.ID, "polar"); len(attached) != 1 || attached[0].ID != second.ID {
		t.Fatalf("attached after detaching = %+v", attached)
	}

	// A detached discount an order already used is still redeemed, once.
	if ok, err := store.RedeemCreditDiscount(ctx, first.ID, "ord-1", 300); err != nil || !ok {
		t.Fatalf("redeem = %v, %v", ok, err)
	}
	if ok, err := store.RedeemCreditDiscount(ctx, first.ID, "ord-2", 100); err != nil || ok {
		t.Fatalf("second redeem = %v, %v; want false", ok, err)
	}
	got, err := store.CreditDiscountByProviderID(ctx, "polar", discountA)
	if err != nil || got.State != state.CreditDiscountRedeemed || got.ProviderInvoiceID != "ord-1" || got.RedeemedCents != 300 || got.SubjectID != acct.ID {
		t.Fatalf("redeemed = %+v, %v", got, err)
	}
	if _, err := store.CreditDiscountByProviderID(ctx, "polar", "promo-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown discount err = %v, want ErrNotFound", err)
	}

	if _, err := store.CreateAccountCredit(ctx, state.AccountCredit{AccountID: other.ID, CentsRemaining: 250, Reason: "goodwill"}); err != nil {
		t.Fatal(err)
	}
	subjects, err := store.CreditDiscountSubjects(ctx, "polar")
	if err != nil || !slices.Contains(subjects, acct.ID) || !slices.Contains(subjects, other.ID) {
		t.Fatalf("subjects = %v, %v; want the attached discount's and the credit's subjects", subjects, err)
	}
}

func TestMemStoreCreditDiscounts(t *testing.T) {
	exerciseCreditDiscounts(t, state.NewMemStore())
}

func TestPgStoreCreditDiscounts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseCreditDiscounts(t, state.NewPgStore(pool))
}
