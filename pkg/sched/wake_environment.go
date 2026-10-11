package sched

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A coordinated wake selects an actual deployed generation before entering
// the leaf coordinator lock. Its detached leader and burst siblings retain
// that selection without using explicit deployment admission's gate bypass.
type wakeEnvironment struct {
	app        state.App
	deployment state.Deployment
	owner      state.RuntimeScalingState
	account    state.Account
	limits     api.Limits
}

// reusableFor reports whether admission may reuse this selection's app,
// account and owner reads. The live deployment is still re-resolved at
// admission, so a cutover between selection and admission stays detected.
func (w wakeEnvironment) reusableFor(appID string) bool {
	return w.app.ID == appID && w.account.ID != "" && w.deployment.ID != ""
}

type wakeEnvironmentKey struct{}

func withWakeEnvironment(ctx context.Context, selected wakeEnvironment) context.Context {
	return context.WithValue(ctx, wakeEnvironmentKey{}, selected)
}

func wakeEnvironmentFrom(ctx context.Context) (wakeEnvironment, bool) {
	selected, ok := ctx.Value(wakeEnvironmentKey{}).(wakeEnvironment)
	return selected, ok
}

func (w wakeEnvironment) coordinatorKey() string {
	return w.app.ID + "\x00deployment:" + w.deployment.ID
}

func (w wakeEnvironment) concurrency(ledger *NodeLedger) int {
	return ledger.servingEnvironmentConcurrency(w.app.ID, runtimeEnvironmentAdmissionKey(w.owner.Scope, w.owner.EnvironmentID), reaperProductionScope(w.owner.Scope))
}

func (e *Engine) resolveWakeEnvironment(ctx context.Context, appID string, loadedApp *state.App) (wakeEnvironment, error) {
	return e.resolveWakeEnvironmentForDeployment(ctx, appID, loadedApp, "")
}

func (e *Engine) resolveWakeEnvironmentForDeployment(ctx context.Context, appID string, loadedApp *state.App, deploymentID string) (wakeEnvironment, error) {
	var selected wakeEnvironment
	var err error
	if loadedApp == nil {
		selected.app, err = e.store.AppByID(ctx, appID)
	} else {
		selected.app = *loadedApp
	}
	if err != nil {
		return selected, fmt.Errorf("sched: wake environment: app: %w", err)
	}
	if selected.app.ID != appID {
		return selected, state.ErrConflict
	}
	account, err := e.store.AccountByID(ctx, selected.app.AccountID)
	if err != nil {
		return selected, fmt.Errorf("sched: wake environment: account: %w", err)
	}
	if !account.Active() {
		return selected, errors.Join(ErrPermanentWake, account.InactiveProblem())
	}
	selected.account = account
	var ok bool
	if selected.limits, ok = api.LimitsFor(account.Plan); !ok {
		return selected, fmt.Errorf("sched: wake environment: unknown plan %q", account.Plan)
	}
	if deploymentID != "" {
		selected.deployment, err = e.store.DeploymentByID(ctx, deploymentID)
		if err == nil && (selected.deployment.AppID != appID || selected.deployment.Status != state.DeployLive ||
			normalizedDeploymentScope(selected.deployment.Scope) != normalizedDeploymentScope(ScopeFrom(ctx))) {
			err = state.ErrNotFound
		}
	} else if ScopeFrom(ctx) == "" {
		selected.deployment, err = state.ResolveProductionDeployment(ctx, e.store, appID)
	} else {
		selected.deployment, err = e.store.LiveDeploymentForScope(ctx, appID, ScopeFrom(ctx))
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return selected, errors.Join(ErrPermanentWake, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "No live deployment", "the app has no live deployment to wake"))
		}
		return selected, fmt.Errorf("sched: wake environment: deployment: %w", err)
	}
	selected.app, err = state.ResolveAppForDeployment(ctx, e.store, selected.app, selected.deployment)
	if err == nil {
		selected.owner, err = e.runtimeScalingStateForDeployment(ctx, selected.app, selected.deployment)
	}
	if err != nil {
		return selected, fmt.Errorf("sched: wake environment: deployed policy: %w", err)
	}
	return selected, nil
}

func checkWakeEnvironmentDeployment(ctx context.Context, appID, deploymentID string) error {
	if selected, ok := wakeEnvironmentFrom(ctx); ok && (selected.app.ID != appID || selected.deployment.ID != deploymentID) {
		return wakeEnvironmentChanged()
	}
	return nil
}

func checkWakeEnvironmentOwner(ctx context.Context, owner state.RuntimeScalingState) error {
	if selected, ok := wakeEnvironmentFrom(ctx); ok && (selected.owner.EnvironmentID != owner.EnvironmentID || selected.owner.Scope != owner.Scope) {
		return wakeEnvironmentChanged()
	}
	return nil
}

func wakeEnvironmentChanged() error {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Wake deployment changed", "retry against the current deployment of the selected environment")
}

func (e *Engine) wakeFanoutForEnvironment(selected wakeEnvironment) WakeFanout {
	fanout := WakeFanout{MaxInFlight: effectiveMaxConcurrency(selected.app, selected.limits), PerVM: selected.limits.ConcurrencyPerVMBound}
	if e.ledger != nil {
		fanout.Existing = selected.concurrency(e.ledger)
	}
	return fanout
}
