package accounttest

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBillingIdentityAcceptsAccountAndOrganizationSubjects(t *testing.T) {
	stores(t, func(t *testing.T, store state.Store) {
		owner := account(t, store)
		org, err := store.CreateOrg(t.Context(), state.Org{Slug: "identity-" + uuid.NewString()[:8], Name: "Identity", Plan: api.PlanFree})
		if err != nil {
			t.Fatal(err)
		}
		for _, subject := range []string{owner.ID, org.ID} {
			t.Run(subject, func(t *testing.T) {
				identity := state.BillingIdentity{AccountID: subject, Provider: "polar", CustomerID: uuid.NewString()}
				if err := store.UpsertBillingIdentity(t.Context(), identity); err != nil {
					t.Fatal("create binding", err)
				}
				created, err := store.BillingIdentity(t.Context(), subject, "polar")
				if err != nil {
					t.Fatal(err)
				}
				for _, subscription := range []string{"sub-created", "sub-renewed", ""} {
					identity.SubscriptionID = subscription
					if err := store.UpsertBillingIdentity(t.Context(), identity); err != nil {
						t.Fatal("update binding", err)
					}
					got, err := store.BillingIdentity(t.Context(), subject, "polar")
					if err != nil || got.CustomerID != identity.CustomerID || got.SubscriptionID != subscription || !got.BillingFrom.Equal(created.BillingFrom) {
						t.Fatalf("binding after update: %+v, %v", got, err)
					}
					resolved, err := store.AccountByBillingCustomerID(t.Context(), "polar", identity.CustomerID)
					if err != nil || resolved.ID != subject {
						t.Fatalf("reverse lookup: %+v, %v", resolved, err)
					}
				}
			})
		}
		if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: uuid.NewString(), Provider: "polar", CustomerID: uuid.NewString()}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("missing subject: %v", err)
		}
	})
}
