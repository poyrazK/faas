package main

import "github.com/onebox-faas/faas/pkg/billing"

// dunningSubscriptions is the provider surface the dunning sweep asks before
// moving an account to Free for non-payment: the billing provider when it
// can list a customer's subscriptions (Polar), else nil (local state only).
func dunningSubscriptions(provider billing.Provider) billing.CustomerSubscriptionLister {
	if provider == nil {
		return nil
	}
	lister, ok := provider.(billing.CustomerSubscriptionLister)
	if !ok {
		return nil
	}
	return lister
}
