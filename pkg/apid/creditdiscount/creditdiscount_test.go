package creditdiscount

// adr: 20261104212028 — credit is a one-time discount on the next order,
// kept in step with the credit, drawn once per order, and carried forward
// when it cannot be attached.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

var renews = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

func ptrTime(t time.Time) *time.Time { return &t }

func TestAvailable(t *testing.T) {
	credits := []state.AccountCredit{
		{CentsRemaining: 500},
		{CentsRemaining: 300, ExpiresAt: ptrTime(renews.Add(time.Hour))},
		{CentsRemaining: 200, ExpiresAt: ptrTime(renews)},
		{CentsRemaining: 0},
	}
	if got := Available(credits, renews); got != 800 {
		t.Fatalf("available = %d, want 800: a credit expiring by the renewal cannot pay it", got)
	}
}

func TestDecide(t *testing.T) {
	accepting := billing.CreditDiscountTarget{Accepting: true, Currency: "usd"}
	attached := &state.CreditDiscount{AmountCents: 500, Currency: "usd"}
	for _, tc := range []struct {
		name      string
		available int64
		target    billing.CreditDiscountTarget
		current   *state.CreditDiscount
		foreign   bool
		want      Plan
	}{
		{"credit and no discount", 500, accepting, nil, false, Plan{Action: Attach, Amount: 500}},
		{"discount matches", 500, accepting, attached, false, Plan{Action: Keep}},
		{"credit changed", 800, accepting, attached, false, Plan{Action: Attach, Amount: 800}},
		{"credit gone", 0, accepting, attached, false, Plan{Action: Detach}},
		{"neither", 0, accepting, nil, false, Plan{Action: Keep}},
		{"another discount", 500, accepting, nil, true, Plan{Action: Hold, Reason: ReasonForeignDiscount}},
		{"not accepting", 500, billing.CreditDiscountTarget{Currency: "usd"}, nil, false, Plan{Action: Hold, Reason: ReasonNotAccepting}},
		{"no currency", 500, billing.CreditDiscountTarget{Accepting: true}, nil, false, Plan{Action: Hold, Reason: ReasonCurrencyUnknown}},
	} {
		if got := Decide(tc.available, tc.target, tc.current, tc.foreign); got != tc.want {
			t.Errorf("%s: plan = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// fakeDiscounter is one Polar subscription "sub-1".
type fakeDiscounter struct {
	target    billing.CreditDiscountTarget
	targetErr error
	attached  string
	setErr    error
	created   map[string]int64
	deleted   []string
	next      int
}

func (f *fakeDiscounter) CreditDiscountTarget(context.Context, string) (billing.CreditDiscountTarget, error) {
	t := f.target
	t.DiscountID = f.attached
	return t, f.targetErr
}

func (f *fakeDiscounter) CreateCreditDiscount(_ context.Context, _ string, amount int64, _ string) (string, error) {
	f.next++
	id := fmt.Sprintf("disc-%d", f.next)
	if f.created == nil {
		f.created = map[string]int64{}
	}
	f.created[id] = amount
	return id, nil
}

func (f *fakeDiscounter) SetSubscriptionDiscount(_ context.Context, _ string, id string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.attached = id
	return nil
}

func (f *fakeDiscounter) DeleteDiscount(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func setup(t *testing.T) (*state.MemStore, *fakeDiscounter, *Syncer, string) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "credit-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: "cus-" + acct.ID, SubscriptionID: "sub-1"}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeDiscounter{target: billing.CreditDiscountTarget{Accepting: true, Currency: "usd", RenewsAt: renews}}
	return store, provider, &Syncer{Store: store, Provider: provider, ProviderName: "polar"}, acct.ID
}

func addCredit(t *testing.T, store *state.MemStore, subject string, cents int64, expires *time.Time) {
	t.Helper()
	if _, err := store.CreateAccountCredit(context.Background(), state.AccountCredit{AccountID: subject, CentsRemaining: cents, Reason: "goodwill", ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
}

func attachedRecords(t *testing.T, store *state.MemStore, subject string) []state.CreditDiscount {
	t.Helper()
	got, err := store.AttachedCreditDiscounts(context.Background(), subject, "polar")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSyncAttachesKeepsAndReplaces(t *testing.T) {
	ctx := context.Background()
	store, provider, syncer, subject := setup(t)
	addCredit(t, store, subject, 500, nil)

	out, err := syncer.Sync(ctx, subject)
	if err != nil || out.Plan.Action != Attach || provider.attached != "disc-1" || provider.created["disc-1"] != 500 {
		t.Fatalf("first sync = %+v, %v; provider %+v", out, err, provider)
	}
	if got := attachedRecords(t, store, subject); len(got) != 1 || got[0].ProviderDiscountID != "disc-1" || got[0].AmountCents != 500 {
		t.Fatalf("records = %+v", got)
	}
	if out, err := syncer.Sync(ctx, subject); err != nil || out.Plan.Action != Keep || provider.next != 1 {
		t.Fatalf("second sync = %+v, %v; it must keep the discount", out, err)
	}

	addCredit(t, store, subject, 300, nil)
	if out, err := syncer.Sync(ctx, subject); err != nil || out.Plan.Action != Attach || provider.attached != "disc-2" || provider.created["disc-2"] != 800 {
		t.Fatalf("sync after more credit = %+v, %v; provider %+v", out, err, provider)
	}
	if got := attachedRecords(t, store, subject); len(got) != 1 || got[0].ProviderDiscountID != "disc-2" {
		t.Fatalf("records = %+v, want only disc-2 attached", got)
	}
	if len(provider.deleted) != 1 || provider.deleted[0] != "disc-1" {
		t.Fatalf("deleted = %v, want the replaced disc-1", provider.deleted)
	}
}

func TestSyncDetachesWhenTheCreditCannotPayTheRenewal(t *testing.T) {
	ctx := context.Background()
	store, provider, syncer, subject := setup(t)
	addCredit(t, store, subject, 500, ptrTime(renews.Add(time.Hour)))
	if _, err := syncer.Sync(ctx, subject); err != nil || provider.attached != "disc-1" {
		t.Fatalf("attach: %v, %+v", err, provider)
	}
	// The next renewal is after the credit expires.
	provider.target.RenewsAt = renews.AddDate(0, 1, 0)
	out, err := syncer.Sync(ctx, subject)
	if err != nil || out.Plan.Action != Detach || provider.attached != "" {
		t.Fatalf("sync = %+v, %v; provider %+v", out, err, provider)
	}
	if got := attachedRecords(t, store, subject); len(got) != 0 {
		t.Fatalf("records = %+v, want none attached", got)
	}
}

func TestSyncCarriesCreditForward(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*fakeDiscounter, *state.MemStore, string)
		want   string
	}{
		{"subscription ending or changing plan", func(f *fakeDiscounter, _ *state.MemStore, _ string) { f.target.Accepting = false }, ReasonNotAccepting},
		{"another discount", func(f *fakeDiscounter, _ *state.MemStore, _ string) { f.attached = "promo-1" }, ReasonForeignDiscount},
		{"subscription gone", func(f *fakeDiscounter, _ *state.MemStore, _ string) { f.targetErr = billing.ErrSubscriptionGone }, ReasonSubscriptionGone},
		{"no subscription", func(_ *fakeDiscounter, store *state.MemStore, subject string) {
			_ = store.UpsertBillingIdentity(context.Background(), state.BillingIdentity{AccountID: subject, Provider: "polar", CustomerID: "cus-" + subject})
		}, ReasonNoSubscription},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, provider, syncer, subject := setup(t)
			addCredit(t, store, subject, 500, nil)
			tc.mutate(provider, store, subject)
			out, err := syncer.Sync(context.Background(), subject)
			if err != nil || out.Plan.Action != Hold || out.Plan.Reason != tc.want || provider.next != 0 {
				t.Fatalf("sync = %+v, %v; created %d discounts, want a hold for %s", out, err, provider.next, tc.want)
			}
		})
	}
}

func TestSyncAttachFailureLeavesNoAttachedDiscount(t *testing.T) {
	store, provider, syncer, subject := setup(t)
	addCredit(t, store, subject, 500, nil)
	provider.setErr = errors.New("polar down")
	if _, err := syncer.Sync(context.Background(), subject); err == nil {
		t.Fatal("sync succeeded although the attach failed")
	}
	if got := attachedRecords(t, store, subject); len(got) != 0 {
		t.Fatalf("records = %+v, want none attached", got)
	}
	if len(provider.deleted) != 1 || provider.deleted[0] != "disc-1" {
		t.Fatalf("deleted = %v, want the unattached disc-1 deleted", provider.deleted)
	}
}

func TestDrawTakesWhatTheOrderUsedOnce(t *testing.T) {
	ctx := context.Background()
	store, provider, syncer, subject := setup(t)
	addCredit(t, store, subject, 1000, nil)
	if _, err := syncer.Sync(ctx, subject); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertInvoice(ctx, state.Invoice{AccountID: subject, Provider: "polar", ProviderInvoiceID: "ord-1",
		Status: "open", PeriodStart: renews, PeriodEnd: renews.AddDate(0, 1, 0), Plan: api.PlanPro, Currency: "usd"}); err != nil {
		t.Fatal(err)
	}
	order := &billing.InvoiceData{ProviderInvoiceID: "ord-1", ProviderDiscountID: provider.attached, DiscountCents: 600}

	drawn, ok, err := Draw(ctx, store, "polar", subject, order)
	if err != nil || !ok || drawn.Result.ConsumedCents != 600 || drawn.Result.RemainingCreditsCents != 400 {
		t.Fatalf("draw = %+v, %v, %v; want 600 drawn, 400 left", drawn, ok, err)
	}
	again, ok, err := Draw(ctx, store, "polar", subject, order)
	if err != nil || !ok || !again.Result.AlreadyConsumedForInvoice || again.Result.RemainingCreditsCents != 400 {
		t.Fatalf("redelivery = %+v, %v, %v; it must draw nothing more", again, ok, err)
	}
	redeemed, err := store.CreditDiscountByProviderID(ctx, "polar", "disc-1")
	if err != nil || redeemed.State != state.CreditDiscountRedeemed || redeemed.RedeemedCents != 600 || redeemed.ProviderInvoiceID != "ord-1" {
		t.Fatalf("discount = %+v, %v", redeemed, err)
	}

	// The subscription still shows the used discount; the rest is attached anew.
	if out, err := syncer.Sync(ctx, subject); err != nil || out.Plan.Action != Attach || provider.created[provider.attached] != 400 {
		t.Fatalf("sync after the draw = %+v, %v; provider %+v", out, err, provider)
	}

	if _, ok, err := Draw(ctx, store, "polar", subject, &billing.InvoiceData{ProviderInvoiceID: "ord-2", ProviderDiscountID: "promo-1", DiscountCents: 100}); ok || err != nil {
		t.Fatalf("an order with another discount drew credit: %v, %v", ok, err)
	}
	if _, _, err := Draw(ctx, store, "polar", uuid.NewString(), order); !errors.Is(err, ErrOtherSubject) {
		t.Fatalf("draw for another subject err = %v, want ErrOtherSubject", err)
	}
}
