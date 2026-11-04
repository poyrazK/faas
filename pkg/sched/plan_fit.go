package sched

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// planFitTTL bounds how long a subject's plan fit stands. A plan or app
// change takes effect within it: a downgrade starts refusing the apps past
// the new plan, and an upgrade or a deleted app lets them wake again.
const planFitTTL = time.Minute

type planFitCache struct {
	mu        sync.Mutex
	bySubject map[string]planFit
}

type planFit struct {
	plan api.Plan
	over map[string]state.PlanOverage
	exp  time.Time
}

// planOverage reports whether app is outside its billing subject's plan, and
// why (ADR-20261103131742). Apps are checked against the plan only when they
// are written, so a plan that ended or shrank without the downgrade preflight
// (a lapsed or revoked subscription, a provider-side change) left every app
// running with the higher plan's deployed-app count and RAM. account is the
// app's resource account (state.AppResourceAccount), whose plan governs. The
// answer is cached per subject and fails open: an app list that cannot be
// read never refuses a wake.
func (e *Engine) planOverage(ctx context.Context, app state.App, account state.Account) (state.PlanOverage, api.Limits, bool) {
	limits, ok := api.LimitsFor(account.Plan)
	if !ok {
		return state.PlanOverage{}, api.Limits{}, false
	}
	subject := account.ResourceOwnerID()
	now := time.Now()
	e.planFits.mu.Lock()
	fit, cached := e.planFits.bySubject[subject]
	e.planFits.mu.Unlock()
	if !cached || fit.plan != account.Plan || now.After(fit.exp) {
		apps, err := state.PlanSubjectApps(ctx, e.store, account)
		if err != nil {
			e.log.Warn("sched: plan fit: list subject apps", "subject", subject, "err", err)
			return state.PlanOverage{}, limits, false
		}
		fit = planFit{plan: account.Plan, over: state.AppsOverPlan(apps, limits), exp: now.Add(planFitTTL)}
		e.planFits.mu.Lock()
		if e.planFits.bySubject == nil {
			e.planFits.bySubject = map[string]planFit{}
		}
		e.planFits.bySubject[subject] = fit
		e.planFits.mu.Unlock()
	}
	over, ok := fit.over[app.ID]
	return over, limits, ok
}

// planFitProblem is the wake refusal for an app outside its subject's plan,
// or nil when it fits.
func (e *Engine) planFitProblem(ctx context.Context, app state.App, account state.Account) *api.Problem {
	over, limits, ok := e.planOverage(ctx, app, account)
	if !ok {
		return nil
	}
	return over.Problem(limits)
}
