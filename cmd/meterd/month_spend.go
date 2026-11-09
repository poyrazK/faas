package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// monthSpend reads an account's month to date for the account_spend_eur
// alert metric: compute, egress and object storage, the billing page's
// figure (ADR-20261109153000).
func monthSpend(store state.Store, provider billing.Provider) alerts.SpendCents {
	return func(ctx context.Context, accountID string) (int64, error) {
		acct, err := store.AccountByID(ctx, accountID)
		if err != nil {
			return 0, err
		}
		return meter.MonthToDateSpendCents(ctx, store, provider, acct, time.Now())
	}
}
