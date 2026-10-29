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
	return newFinancialBudgetAdmissionWithGrace(store, api.FinancialBudgetAdmissionOutageGrace)
}

func newFinancialBudgetAdmissionWithGrace(store state.Store, grace time.Duration) func(context.Context, gateway.App) error {
	cache := newBudgetAdmissionCache(time.Now, grace)
	return func(ctx context.Context, routed gateway.App) error {
		scope := routed.Scope
		if routed.PinnedDeploymentScope != "" {
			scope = routed.PinnedDeploymentScope
		}
		return cache.check(ctx, budgetAdmissionKey{app: routed.ID, scope: scope, deployment: routed.PinnedDeploymentID}, func(ctx context.Context) error {
			app, err := store.AppByID(ctx, routed.ID)
			if err != nil {
				return err
			}
			return state.CheckFinancialBudgetAdmissionForScope(ctx, store, app, scope, routed.PinnedDeploymentID, time.Now().UTC())
		})
	}
}

func financialBudgetServiceAdmission(store state.Store) func(context.Context, string, string) error {
	cache := newBudgetAdmissionCache(time.Now, api.FinancialBudgetAdmissionOutageGrace)
	return func(ctx context.Context, appID, deploymentID string) error {
		return cache.check(ctx, budgetAdmissionKey{app: appID, deployment: deploymentID}, func(ctx context.Context) error {
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
	at         time.Time // when the store last answered
	err        error     // nil when admitted, the held problem when held
	refreshing bool
}

// budgetAdmissionCache keeps budget admission off the request path
// (ADR-20261028083324). A decision younger than
// api.FinancialBudgetAdmissionCacheTTL is served as is. An older one, still
// within the outage grace, is served while one background read per key
// refreshes it, so warm traffic never waits for Postgres. Only a successful
// read replaces a decision: while the store cannot be read the last decision
// keeps answering until the grace, and a held decision is never turned into an
// admission. A key with no decision, or one older than the grace, reads on the
// request and fails closed as before.
type budgetAdmissionCache struct {
	mu    sync.Mutex
	now   func() time.Time
	grace time.Duration
	spawn func(func())
	rows  map[budgetAdmissionKey]budgetAdmissionDecision
}

func newBudgetAdmissionCache(now func() time.Time, grace time.Duration) *budgetAdmissionCache {
	return &budgetAdmissionCache{now: now, grace: grace, spawn: func(f func()) { go f() },
		rows: map[budgetAdmissionKey]budgetAdmissionDecision{}}
}

func (c *budgetAdmissionCache) check(ctx context.Context, key budgetAdmissionKey, read func(context.Context) error) error {
	now := c.now()
	c.mu.Lock()
	if d, ok := c.rows[key]; ok && !d.at.After(now) && now.Sub(d.at) < max(c.grace, api.FinancialBudgetAdmissionCacheTTL) {
		refresh := now.Sub(d.at) >= api.FinancialBudgetAdmissionCacheTTL && !d.refreshing
		if refresh {
			d.refreshing = true
			c.rows[key] = d
		}
		c.mu.Unlock()
		if refresh {
			// The request ends before the refresh does; keep its values only.
			detached := context.WithoutCancel(ctx)
			c.spawn(func() { c.refresh(detached, key, read) })
		}
		return d.err
	}
	c.mu.Unlock()
	err := read(ctx)
	c.record(key, now, err)
	return err
}

// refresh re-reads one decision off the request path.
func (c *budgetAdmissionCache) refresh(ctx context.Context, key budgetAdmissionKey, read func(context.Context) error) {
	ctx, cancel := context.WithTimeout(ctx, api.FinancialBudgetAdmissionRefreshTimeout)
	defer cancel()
	err := read(ctx)
	c.record(key, c.now(), err)
}

// record stores a decision, drops the key when the store answered that the
// admission cannot be evaluated (the app is gone or the read is inconsistent),
// and otherwise keeps the last decision: an unavailable store is never cached.
func (c *budgetAdmissionCache) record(key budgetAdmissionKey, at time.Time, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case err == nil || errors.Is(err, state.ErrFinancialBudgetHeld):
		if _, ok := c.rows[key]; !ok && len(c.rows) >= api.FinancialBudgetAdmissionCacheSize {
			clear(c.rows)
		}
		c.rows[key] = budgetAdmissionDecision{at: at, err: err}
	case errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrConflict):
		delete(c.rows, key)
	default:
		if d, ok := c.rows[key]; ok {
			d.refreshing = false
			c.rows[key] = d
		}
	}
}
