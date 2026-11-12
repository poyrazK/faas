package floor

import (
	"context"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// floorReuseMaxAge bounds how long the per-deployment sweep reuses a
// deployment's resolved settings (its migration fence and project workload
// spec) and an account's plan while the deployment's floor holds (ADR
// 20261103094355). The sweep ticks every second on every scheduler and read
// the app, the fence, the spec and the account for each floored deployment,
// although the floor is met almost every time. Before admitting it resolves
// all of them again, so reuse only delays noticing a raised floor or a
// changed plan by up to this long.
const floorReuseMaxAge = 10 * time.Second

// AppsByIDReader is an optional AppStore extension that copies only the
// listed apps among ids, keyed by ID. schedd's app snapshot has it.
type AppsByIDReader interface {
	AppsByID(ctx context.Context, nodeID string, ids []string) (map[string]state.App, error)
}

type resolvedDeployment struct {
	base  state.App // the listed app the resolution started from, stamps cleared
	scope string
	app   state.App
	at    time.Time
}

type resolvedPlan struct {
	plan api.Plan
	at   time.Time
}

type deploymentEvaluation struct {
	decision  Decision
	effective int
	conc      int
	skip      bool // not this trigger's app, or no floor runs for it
}

// listedApps returns the parent apps of deps from the trigger's app list, or
// nil when there is none; each missing app is then read on its own.
func (t *Trigger) listedApps(ctx context.Context, deps []state.Deployment) map[string]state.App {
	if t.appStore == nil || len(deps) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(deps))
	ids := make([]string, 0, len(deps))
	for _, d := range deps {
		if _, dup := seen[d.AppID]; !dup && d.Status == state.DeployLive {
			seen[d.AppID] = struct{}{}
			ids = append(ids, d.AppID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if reader, ok := t.appStore.(AppsByIDReader); ok {
		apps, err := reader.AppsByID(ctx, t.ownerNodeID, ids)
		if err != nil {
			t.log.Warn("floor: list floored apps; reading each", "err", err)
			return nil
		}
		return apps
	}
	var apps []state.App
	var err error
	if t.ownerNodeID != "" {
		apps, err = t.appStore.ListAppsByNodeID(ctx, t.ownerNodeID)
	} else {
		apps, err = t.appStore.ListAllApps(ctx)
	}
	if err != nil {
		t.log.Warn("floor: list apps; reading each", "err", err)
		return nil
	}
	out := make(map[string]state.App, len(ids))
	for _, app := range apps {
		if _, ok := seen[app.ID]; ok {
			out[app.ID] = app
		}
	}
	return out
}

// deploymentApp returns d's app with d's settings applied, and whether it was
// read from the store just now. A listed app reuses its resolution while the
// listed row and d's scope are unchanged and the resolution is younger than
// floorReuseMaxAge; its scale stamps come from the list, which can be older
// than the row, so an admission resolves again from the store.
func (t *Trigger) deploymentApp(ctx context.Context, d state.Deployment, listed map[string]state.App, now time.Time) (state.App, bool, error) {
	base, ok := listed[d.AppID]
	if !ok {
		app, err := state.AppForDeployment(ctx, t.deploymentStore, d)
		return app, true, err
	}
	key := withoutScaleStamps(base)
	t.reuseMu.Lock()
	entry, hit := t.resolutions[d.ID]
	t.reuseMu.Unlock()
	if hit && now.Sub(entry.at) < floorReuseMaxAge && entry.scope == d.Scope && reflect.DeepEqual(entry.base, key) {
		app := entry.app
		app.LastScaleInAt, app.LastScaleOutAt = base.LastScaleInAt, base.LastScaleOutAt
		return app, false, nil
	}
	app, err := state.ResolveAppForDeployment(ctx, t.deploymentStore, base, d)
	if err != nil {
		t.forgetResolution(d.ID)
		return app, false, err
	}
	t.reuseMu.Lock()
	if t.resolutions == nil {
		t.resolutions = map[string]resolvedDeployment{}
	}
	t.resolutions[d.ID] = resolvedDeployment{base: key, scope: d.Scope, app: app, at: now}
	t.reuseMu.Unlock()
	return app, false, nil
}

func withoutScaleStamps(app state.App) state.App {
	app.LastScaleInAt, app.LastScaleOutAt = nil, nil
	return app
}

func (t *Trigger) forgetResolution(deploymentID string) {
	t.reuseMu.Lock()
	delete(t.resolutions, deploymentID)
	t.reuseMu.Unlock()
}

// pruneReuse drops resolutions and plans too old to be reused.
func (t *Trigger) pruneReuse(now time.Time) {
	t.reuseMu.Lock()
	defer t.reuseMu.Unlock()
	for id, entry := range t.resolutions {
		if now.Sub(entry.at) >= floorReuseMaxAge {
			delete(t.resolutions, id)
		}
	}
	for id, entry := range t.plans {
		if now.Sub(entry.at) >= floorReuseMaxAge {
			delete(t.plans, id)
		}
	}
}

// appPlan resolves the plan of app's billing subject (resolvePlan), reusing a
// resolution younger than floorReuseMaxAge unless fresh. It is kept per app,
// because a company app's subject is its organization, not its account. A
// Free result, which is also what a failed resolution yields, is not kept.
func (t *Trigger) appPlan(ctx context.Context, app state.App, now time.Time, fresh bool) api.Plan {
	if t.planResolver == nil {
		return api.PlanFree
	}
	if !fresh {
		t.reuseMu.Lock()
		entry, hit := t.plans[app.ID]
		t.reuseMu.Unlock()
		if hit && now.Sub(entry.at) < floorReuseMaxAge {
			return entry.plan
		}
	}
	plan := t.resolvePlan(ctx, app)
	if plan == api.PlanFree {
		return plan
	}
	t.reuseMu.Lock()
	if t.plans == nil {
		t.plans = map[string]resolvedPlan{}
	}
	t.plans[app.ID] = resolvedPlan{plan: plan, at: now}
	t.reuseMu.Unlock()
	return plan
}

// evaluateDeployment decides d's floor against app. fresh resolves the plan
// from the store instead of reusing it.
func (t *Trigger) evaluateDeployment(ctx context.Context, d state.Deployment, app state.App, serving map[string]bool, now time.Time, residentRAM, headroom int, fresh bool) deploymentEvaluation {
	if !t.manages(app) || !floorRunsFor(app) {
		return deploymentEvaluation{skip: true}
	}
	disabled := deploymentEvaluation{decision: Decision{Outcome: OutcomeDisabled}}
	effective := app.EffectiveMinInstances()
	if inheritsNoFloor(d, serving) {
		effective = 0
	}
	if dFloor := d.EffectiveMinInstances(); dFloor > effective {
		effective = dFloor
	}
	if effective <= 0 {
		return disabled
	}
	plan := t.appPlan(ctx, app, now, fresh)
	if !plan.MinInstancesAllowed() || app.WorkloadClass == state.WorkloadClassWorker {
		return disabled
	}
	// ADR-20261105120000: a floor saved under a larger plan is capped by the
	// current one.
	effective = min(effective, plan.MaxMinInstances())
	if effective <= 0 {
		return disabled
	}
	var conc int
	if t.ledger != nil {
		conc = t.ledger.ConcurrencyForDeployment(d.AppID, d.ID)
	}
	if conc >= effective {
		return deploymentEvaluation{decision: Decision{Outcome: OutcomeFloorMet}, effective: effective, conc: conc}
	}
	if conc >= effectiveMaxConcurrency(app, plan) {
		return deploymentEvaluation{decision: Decision{Outcome: OutcomeAtCapacity}, effective: effective, conc: conc}
	}
	// RAM ceiling pre-check (same as tickPerApp).
	isRamCeiling := t.ledger != nil && api.BillableRAMMB(app.RAMMB) > headroom
	var lastScaleOut time.Time
	if app.LastScaleOutAt != nil {
		lastScaleOut = *app.LastScaleOutAt
	}
	stats := AppStats{
		AppID:             d.AppID,
		AccountID:         app.AccountID,
		Plan:              plan,
		Floor:             effective,
		Concurrency:       conc,
		MaxConcurrency:    effectiveMaxConcurrency(app, plan),
		ResidentRAMMB:     residentRAM,
		HeadroomMB:        headroom,
		RAMMB:             app.RAMMB,
		WorkloadClass:     app.WorkloadClass,
		LastScaleOutAt:    lastScaleOut,
		ScaleOutCooldownS: scalingOutCooldownS(app),
		Now:               now,
		BackoffUntil:      t.peekBackoff(d.AppID),
		IsRamCeiling:      isRamCeiling,
	}
	return deploymentEvaluation{decision: decide(stats), effective: effective, conc: conc}
}
