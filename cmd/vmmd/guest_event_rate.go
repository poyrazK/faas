package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// guestEventPlanTTL is how long a node trusts a cached account plan; a plan
// change reaches the in-guest publish rate within it.
const guestEventPlanTTL = time.Minute

// guestEventRate applies the per-account event publish rate
// (api.EventPublishLimits, ADR-20261026140000) to in-guest publishes on this
// node, as apid does for the publish routes. It caches each account's plan so
// a publish does not read the account row.
type guestEventRate struct {
	limiter events.PublishLimiter
	mu      sync.Mutex
	plans   map[string]guestEventPlan
}

type guestEventPlan struct {
	plan api.Plan
	at   time.Time
}

type guestEventAccounts interface {
	AccountByID(context.Context, string) (state.Account, error)
}

// allow charges one event to the account, refusing it with a retryable error
// when the account is over its rate. A store that cannot read accounts, or
// an unknown plan, is not limited.
func (g *guestEventRate) allow(ctx context.Context, store any, accountID string) error {
	accounts, ok := store.(guestEventAccounts)
	if !ok {
		return nil
	}
	plan, err := g.plan(ctx, accounts, accountID)
	if err != nil {
		return fmt.Errorf("read account plan for event publish rate: %w", err)
	}
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return nil
	}
	if wait, ok := g.limiter.Allow(accountID, limits.EventPublish, 1); !ok {
		return fmt.Errorf("event publish rate limited: the account publishes at most %d events per second; retry the same event after %s",
			limits.EventPublish.PerSecond, wait.Round(time.Millisecond))
	}
	return nil
}

func (g *guestEventRate) plan(ctx context.Context, accounts guestEventAccounts, accountID string) (api.Plan, error) {
	g.mu.Lock()
	cached, ok := g.plans[accountID]
	g.mu.Unlock()
	if ok && time.Since(cached.at) < guestEventPlanTTL {
		return cached.plan, nil
	}
	account, err := accounts.AccountByID(ctx, accountID)
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	if g.plans == nil || len(g.plans) >= 10000 {
		g.plans = map[string]guestEventPlan{}
	}
	g.plans[accountID] = guestEventPlan{plan: account.Plan, at: time.Now()}
	g.mu.Unlock()
	return account.Plan, nil
}
