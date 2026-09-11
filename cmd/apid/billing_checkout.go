package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// errNoHostedCheckout is returned by beginHostedCheckout when the active
// provider cannot materialize a hosted checkout for the account: no
// provider is wired, the provider lacks CapHostedCheckout, the account
// already has a subscription (the provider portal owns product changes
// on an existing subscription), the target is not a paid upgrade, or the
// provider answered with the ("", "", nil) "no hosted checkout" sentinel.
// Callers fall back to the billing portal URL.
var errNoHostedCheckout = errors.New("apid: hosted checkout unavailable")

// hostedCheckoutAvailable reports whether beginHostedCheckout can start a
// checkout for acct → plan. Shared by PATCH /v1/account/plan and the
// dashboard upgrade page so both surfaces agree on the gate.
func (s *server) hostedCheckoutAvailable(acct state.Account, plan api.Plan) bool {
	return s.billingProvider != nil &&
		s.billingProvider.Capabilities().Has(billing.CapHostedCheckout) &&
		acct.Plan.RequiresBillingUpgradeTo(plan)
}

// beginHostedCheckout ensures the provider customer exists, stamps its ID
// on the account row, and creates the provider's hosted checkout for
// plan. It is the single code path behind the 402 checkout_url on
// PATCH /v1/account/plan and the dashboard's POST /dashboard/upgrade so
// the CLI and the browser cannot drift.
//
// Customer creation is idempotent: when acct.ProviderCustomerID is
// already set the provider call is skipped, and the Polar / Paddle
// implementations look the customer up by external ID before creating
// one, so a redelivered click does not orphan a second provider
// customer.
//
// Returns errNoHostedCheckout when the gate in hostedCheckoutAvailable
// is closed or the provider returned the no-checkout sentinel; every
// other error is a provider or store failure the caller should map to
// 503.
func (s *server) beginHostedCheckout(ctx context.Context, acct state.Account, plan api.Plan) (txID, checkoutURL string, err error) {
	if !s.hostedCheckoutAvailable(acct, plan) {
		return "", "", errNoHostedCheckout
	}
	provider := providerName(s.billingProvider)
	identity, identityErr := s.store.BillingIdentity(ctx, acct.ID, provider)
	if identityErr != nil && !errors.Is(identityErr, state.ErrNotFound) {
		return "", "", fmt.Errorf("load provider billing identity: %w", identityErr)
	}
	if identityErr == nil {
		if identity.SubscriptionID != "" {
			return "", "", errNoHostedCheckout
		}
		acct.ProviderCustomerID = identity.CustomerID
		acct.StripeSubscriptionItem = identity.SubscriptionID
	} else {
		// Clear another provider's compatibility-cache handles before asking
		// this backend to resolve/create by Gregale external account ID.
		acct.ProviderCustomerID = ""
		acct.StripeSubscriptionItem = ""
		custID, cerr := s.billingProvider.CreateCustomer(ctx, acct)
		if cerr != nil {
			s.log.Error("create_customer",
				"account", acct.ID,
				"target_plan", logsanitize.Field(string(plan)),
				"err", cerr)
			return "", "", fmt.Errorf("create provider customer: %w", cerr)
		}
		identity = state.BillingIdentity{AccountID: acct.ID, Provider: provider, CustomerID: custID}
		if err := s.store.UpsertBillingIdentity(ctx, identity); err != nil {
			return "", "", fmt.Errorf("stamp provider-qualified customer id: %w", err)
		}
		if err := s.store.UpdateAccountProviderCustomerID(ctx, acct.ID, custID); err != nil {
			// The provider-side customer exists but the local binding
			// failed. Polar and Paddle both resolve the customer by
			// external ID on the next attempt, so the retry reuses it;
			// the structured field lets an operator find any orphan
			// on the provider dashboard.
			s.log.Error("stamp_customer_id",
				"account", acct.ID,
				"customer_id", custID,
				"orphan_paddle_customer", true,
				"err", err)
			return "", "", fmt.Errorf("stamp provider customer id: %w", err)
		}
		if err := s.store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, ""); err != nil {
			return "", "", fmt.Errorf("clear stale provider subscription id: %w", err)
		}
		acct.ProviderCustomerID = custID
	}
	txID, checkoutURL, err = s.billingProvider.CreateUpgradeTransaction(ctx, acct, plan)
	if err != nil {
		s.log.Error("create_upgrade_tx",
			"account", acct.ID,
			"target_plan", logsanitize.Field(string(plan)),
			"err", err)
		return "", "", fmt.Errorf("create upgrade transaction: %w", err)
	}
	if txID == "" || checkoutURL == "" {
		return "", "", errNoHostedCheckout
	}
	return txID, checkoutURL, nil
}
