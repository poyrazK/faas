// adr: 20261122200000

package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SetDevPrimeServes applies runtime config dev_prime_serves_enabled
// (ADR-20261122200000).
func (e *Engine) SetDevPrimeServes(enabled bool) { e.devPrimeServes.Store(enabled) }

// serveDeveloperPrime lets the healthy prime of a `gregale dev` deployment
// stay RUNNING and serve, instead of capturing, publishing and parking it
// before promotion (ADR-20261122200000). It reports false, with nothing
// changed, whenever the deployment must take the ordinary snapshot prime:
// the setting is off, the deployment is not a developer environment's own
// request or service deployment, or the app's serving concurrency (with the
// rollout grant, ADR-199) has no room for the prime. The caller holds the
// app lock.
func (e *Engine) serveDeveloperPrime(ctx context.Context, app state.App, limits api.Limits, dep state.Deployment, ins state.Instance) bool {
	mode := executionModeForApp(app)
	if !e.devPrimeServes.Load() || !state.DeveloperPrimeEligible(app, dep) ||
		(mode != api.ExecutionModeRequest && mode != api.ExecutionModeService) {
		return false
	}
	if branched, err := e.environmentBranchDeployment(ctx, dep.ID); err != nil || branched {
		return false
	}
	// Rehearsal runs (ADR-20261017201013) seed developer sessions and
	// expect the prime's snapshot.
	if _, err := e.store.ScenarioTestMemberByApp(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		return false
	}
	values, err := e.loadRuntimeDeploymentValues(ctx, app, dep)
	if err != nil {
		e.log.Warn("sched: developer prime: load runtime values; parking as usual", "app", app.ID, "deployment", dep.ID, "err", err)
		return false
	}
	environmentKey := runtimeEnvironmentAdmissionKey(values.Snapshot.Scope, values.Snapshot.EnvironmentID)
	production := reaperProductionScope(values.Snapshot.Scope)
	if _, _, refused := e.wakeServingCapacity(app, limits, dep.ID, environmentKey, production).refusal(); refused {
		return false
	}
	// The prime was admitted as KindSnapshotPrime, outside serving
	// concurrency; from here it is a serving replica (invariant 1).
	if !e.ledger.PromoteWarm(ins.ID) {
		return false
	}
	if err := e.emitDeploymentReady(ctx, dep.ID, mode, ins.ID, state.DeveloperPrimeActivationReason); err != nil {
		e.log.Warn("sched: developer prime: activation not sent; parking as usual", "app", app.ID, "deployment", dep.ID, "err", err)
		e.ledger.BeginSnapshot(ins.ID)
		return false
	}
	e.log.Info("sched: developer prime serves without a snapshot", "app", app.ID, "deployment", dep.ID, "instance", ins.ID)
	return true
}
