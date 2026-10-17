package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func newFinancialBudgetAdmission(store state.Store) func(context.Context, gateway.App) error {
	return func(ctx context.Context, routed gateway.App) error {
		app, err := store.AppByID(ctx, routed.ID)
		if err != nil {
			return err
		}
		scope := routed.Scope
		if routed.PinnedDeploymentScope != "" {
			scope = routed.PinnedDeploymentScope
		}
		return state.CheckFinancialBudgetAdmissionForScope(ctx, store, app, scope, routed.PinnedDeploymentID, time.Now().UTC())
	}
}

func financialBudgetServiceAdmission(store state.Store) func(context.Context, string, string) error {
	return func(ctx context.Context, appID, deploymentID string) error {
		app, err := store.AppByID(ctx, appID)
		if err != nil {
			return err
		}
		return state.CheckFinancialBudgetAdmissionForScope(ctx, store, app, "", deploymentID, time.Now().UTC())
	}
}
