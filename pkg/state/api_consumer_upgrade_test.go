package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func checkAPIConsumerUpgrade(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	upgrades := st.(state.APIConsumerUpgradeStore)
	cardless, err := st.CreateAPIConsumer(ctx, accountID, appID, "oauth:abc", "Ada")
	if err != nil {
		t.Fatal(err)
	}
	paying, err := st.CreateAPIConsumer(ctx, accountID, appID, "cus_paying", "Bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upgrades.AttachAPIConsumerStripeCustomer(ctx, accountID, cardless.ID, "not-a-customer"); err == nil {
		t.Fatal("a non-Stripe ref was attached")
	}
	if _, err := upgrades.AttachAPIConsumerStripeCustomer(ctx, accountID, cardless.ID, "cus_paying"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("another consumer's customer = %v; want conflict", err)
	}
	got, err := upgrades.AttachAPIConsumerStripeCustomer(ctx, accountID, cardless.ID, "cus_new")
	if err != nil || got.ExternalRef != "cus_new" || got.ID != cardless.ID {
		t.Fatalf("attach = %+v %v", got, err)
	}
	if again, err := upgrades.AttachAPIConsumerStripeCustomer(ctx, accountID, cardless.ID, "cus_new"); err != nil || again.ExternalRef != "cus_new" {
		t.Fatalf("attach again = %+v %v; want a no-op", again, err)
	}
	if _, err := upgrades.AttachAPIConsumerStripeCustomer(ctx, accountID, cardless.ID, "cus_other"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replacing a Stripe customer = %v; want conflict", err)
	}
	if _, err := upgrades.AttachAPIConsumerStripeCustomer(ctx, accountID, paying.ID, "cus_other"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("a paying consumer = %v; want conflict", err)
	}
	if _, err := upgrades.AttachAPIConsumerStripeCustomer(ctx, "00000000-0000-4000-8000-000000000009", cardless.ID, "cus_new"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account = %v; want not found", err)
	}
}

// adr: 20261124094745
func TestMemStoreAPIConsumerUpgrade(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerUpgrade(t, ctx, store, accountID, appID)
}

// adr: 20261124094745
func TestPgStoreAPIConsumerUpgrade(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerUpgrade(t, ctx, store, accountID, appID)
}
