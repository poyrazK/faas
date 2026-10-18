package object

import (
	"context"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

type storageBillingAccounts struct {
	store billing.ObjectStorageAccountSource
}

func (a storageBillingAccounts) ListAllAccounts(ctx context.Context) ([]state.Account, error) {
	return state.ListBillingResourceAccounts(ctx, a.store)
}
