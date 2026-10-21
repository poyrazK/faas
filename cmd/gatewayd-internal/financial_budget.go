package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func newFinancialBudgetAdmission(store state.Store) func(context.Context, gateway.App) error {
	cache := newBudgetAdmissionCache(time.Now)
	return func(ctx context.Context, routed gateway.App) error {
		scope := routed.Scope
		if routed.PinnedDeploymentScope != "" {
			scope = routed.PinnedDeploymentScope
		}
		return cache.check(budgetAdmissionKey{app: routed.ID, scope: scope, deployment: routed.PinnedDeploymentID}, func() error {
			app, err := store.AppByID(ctx, routed.ID)
			if err != nil {
				return err
			}
			return state.CheckFinancialBudgetAdmissionForScope(ctx, store, app, scope, routed.PinnedDeploymentID, time.Now().UTC())
		})
	}
}

func financialBudgetServiceAdmission(store state.Store) func(context.Context, string, string) error {
	cache := newBudgetAdmissionCache(time.Now)
	return func(ctx context.Context, appID, deploymentID string) error {
		return cache.check(budgetAdmissionKey{app: appID, deployment: deploymentID}, func() error {
			app, err := store.AppByID(ctx, appID)
			if err != nil {
				return err
			}
			return state.CheckFinancialBudgetAdmissionForScope(ctx, store, app, "", deploymentID, time.Now().UTC())
		})
	}
}

type budgetAdmissionKey struct{ app, scope, deployment string }

type budgetAdmissionDecision struct {
	at  time.Time
	err error
}

// budgetAdmissionCache keeps warm traffic off the database for every request.
// Only decisions are cached, admitted or held; an unavailable store is
// retried on the next request and still fails closed.
type budgetAdmissionCache struct {
	mu   sync.Mutex
	now  func() time.Time
	rows map[budgetAdmissionKey]budgetAdmissionDecision
}

func newBudgetAdmissionCache(now func() time.Time) *budgetAdmissionCache {
	return &budgetAdmissionCache{now: now, rows: map[budgetAdmissionKey]budgetAdmissionDecision{}}
}

func (c *budgetAdmissionCache) check(key budgetAdmissionKey, read func() error) error {
	now := c.now()
	c.mu.Lock()
	if d, ok := c.rows[key]; ok && now.Sub(d.at) < api.FinancialBudgetAdmissionCacheTTL && !d.at.After(now) {
		c.mu.Unlock()
		return d.err
	}
	c.mu.Unlock()
	err := read()
	if err != nil && !errors.Is(err, state.ErrFinancialBudgetHeld) {
		return err
	}
	c.mu.Lock()
	if len(c.rows) >= api.FinancialBudgetAdmissionCacheSize {
		clear(c.rows)
	}
	c.rows[key] = budgetAdmissionDecision{at: now, err: err}
	c.mu.Unlock()
	return err
}
