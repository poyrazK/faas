package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// lookupBillingAccount resolves a webhook inside its provider namespace. The
// legacy fallback is a one-time adoption path for upgraded installations.
func (s *server) lookupBillingAccount(ctx context.Context, provider, customerID string) (state.Account, error) {
	if customerID == "" {
		return state.Account{}, fmt.Errorf("apid: empty %s customer id", provider)
	}
	acct, err := s.store.AccountByBillingCustomerID(ctx, provider, customerID)
	if err == nil {
		identity, identityErr := s.store.BillingIdentity(ctx, acct.ID, provider)
		if identityErr != nil {
			return state.Account{}, identityErr
		}
		acct.ProviderCustomerID = identity.CustomerID
		acct.StripeSubscriptionItem = identity.SubscriptionID
		return acct, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.Account{}, err
	}
	if !legacyCustomerMatchesProvider(provider, customerID) {
		return state.Account{}, state.ErrNotFound
	}
	acct, err = s.store.AccountByProviderCustomerID(ctx, customerID)
	if err != nil {
		return state.Account{}, err
	}
	if err := s.store.UpsertBillingIdentity(ctx, state.BillingIdentity{
		AccountID: acct.ID, Provider: provider, CustomerID: customerID,
		SubscriptionID: acct.StripeSubscriptionItem,
	}); err != nil {
		return state.Account{}, fmt.Errorf("adopt legacy billing identity: %w", err)
	}
	return acct, nil
}

func legacyCustomerMatchesProvider(provider, customerID string) bool {
	switch provider {
	case "stripe":
		return strings.HasPrefix(customerID, "cus_")
	case "paddle":
		return strings.HasPrefix(customerID, "ctm_")
	case "polar":
		return !strings.HasPrefix(customerID, "cus_") && !strings.HasPrefix(customerID, "ctm_")
	default:
		return false
	}
}

// accountForActiveBillingProvider overlays the configured provider's exact
// identity on the legacy Account compatibility fields. Missing identities are
// returned as empty fields, never as another provider's stale handles.
func (s *server) accountForActiveBillingProvider(ctx context.Context, acct state.Account) (state.Account, error) {
	provider, qualified := providerIdentityName(s.billingProvider)
	if !qualified {
		return acct, nil
	}
	acct.ProviderCustomerID = ""
	acct.StripeSubscriptionItem = ""
	identity, err := s.store.BillingIdentity(ctx, acct.ID, provider)
	if errors.Is(err, state.ErrNotFound) {
		return acct, nil
	}
	if err != nil {
		return acct, err
	}
	acct.ProviderCustomerID = identity.CustomerID
	acct.StripeSubscriptionItem = identity.SubscriptionID
	return acct, nil
}

// stampActiveBillingSubscription updates the qualified source of truth before
// its legacy compatibility cache.
func (s *server) stampActiveBillingSubscription(ctx context.Context, acct state.Account, subscriptionID string) error {
	provider := providerName(s.billingProvider)
	if provider == "" {
		// Narrow unit-test/no-provider compatibility path. Production webhook
		// handlers reject a missing active provider before reaching here.
		return s.store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, subscriptionID)
	}
	identity, err := s.store.BillingIdentity(ctx, acct.ID, provider)
	if errors.Is(err, state.ErrNotFound) && acct.ProviderCustomerID != "" {
		identity = state.BillingIdentity{AccountID: acct.ID, Provider: provider, CustomerID: acct.ProviderCustomerID}
		err = nil
	}
	if err != nil {
		return err
	}
	if identity.CustomerID == "" {
		return errors.New("billing identity has no customer id")
	}
	identity.SubscriptionID = subscriptionID
	if err := s.store.UpsertBillingIdentity(ctx, identity); err != nil {
		return err
	}
	if err := s.store.UpdateAccountProviderCustomerID(ctx, acct.ID, identity.CustomerID); err != nil {
		return err
	}
	return s.store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, subscriptionID)
}
