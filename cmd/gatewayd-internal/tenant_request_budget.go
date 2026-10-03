package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type tenantRequestBudgetStore struct{ store *state.PgStore }

func newTenantRequestBudgetStore(store *state.PgStore) gateway.TenantRequestBudgetStore {
	if store == nil {
		return nil
	}
	return tenantRequestBudgetStore{store: store}
}

func (a tenantRequestBudgetStore) AdmitTenantRequest(ctx context.Context, accountID, tenantID string) (gateway.TenantRequestBudgetDecision, error) {
	decision, err := a.store.AdmitPlatformTenantRequest(ctx, accountID, tenantID)
	return gateway.TenantRequestBudgetDecision{
		Allowed: decision.Allowed, Scope: decision.Scope, Limit: decision.Limit,
		Observed: decision.Observed, RetryAfterSeconds: decision.RetryAfterSeconds,
	}, err
}
