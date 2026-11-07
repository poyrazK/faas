// Package dataapiworkload decides which Data API runtimes may report to the
// platform. Every private runtime route (usage, change events, change hook
// status) accepts only a workload token from a live production instance of an
// active app (ADR-20261019102108 amendment 2026-11-05, ADR-20261022121642 amendment 2).
package dataapiworkload

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Store resolves the workload a token names.
type Store interface {
	state.AppOwnerReader
	AppByID(ctx context.Context, id string) (state.App, error)
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
	DeploymentByID(ctx context.Context, id string) (state.Deployment, error)
}

// ErrNotProduction marks a valid token from a stage, preview or cloned
// environment. Those runtimes share the production app's id, so their usage,
// access settings and changes would land in production's rows. The refusal is
// final: a runtime stops reporting instead of retrying it.
var ErrNotProduction = errors.New("the workload does not run a production deployment")

// errUntrusted is every other refusal: no trust configured, a bad token, or an
// app, account or instance that is gone. A runtime retries these.
var errUntrusted = errors.New("workload identity required")

// Live verifies a token for audience from a live production instance and
// resolves its app and the account its resources belong to.
func Live(r *http.Request, verifier *workloadidentity.Verifier, audience string, store Store, now time.Time) (state.Account, state.App, error) {
	if verifier == nil || store == nil {
		return state.Account{}, state.App{}, errUntrusted
	}
	claims, err := verifier.Verify(Bearer(r), audience, now)
	if err != nil {
		return state.Account{}, state.App{}, errUntrusted
	}
	app, err := store.AppByID(r.Context(), claims.AppID)
	if err != nil || app.AccountID != claims.AccountID || app.Status == state.AppDeleted {
		return state.Account{}, state.App{}, errUntrusted
	}
	acct, err := state.AppResourceAccount(r.Context(), store, app)
	if err != nil || !acct.Active() {
		return state.Account{}, state.App{}, errUntrusted
	}
	instance, err := store.InstanceByID(r.Context(), claims.InstanceID)
	if err != nil || instance.AppID != app.ID || !state.State(instance.State).CountsForConcurrency() {
		return state.Account{}, state.App{}, errUntrusted
	}
	// Only production reports, as only production leases queues (ADR-1141
	// amendment 2026-10-27, bug hunt 36).
	if !state.ProductionDeploymentScope(claims.Environment) {
		return state.Account{}, state.App{}, ErrNotProduction
	}
	deployment, err := store.DeploymentByID(r.Context(), instance.DeploymentID)
	if err != nil || deployment.AppID != app.ID {
		return state.Account{}, state.App{}, errUntrusted
	}
	if !state.ProductionDeploymentScope(deployment.Scope) {
		return state.Account{}, state.App{}, ErrNotProduction
	}
	return acct, app, nil
}

// Refuse writes the problem for a Live error: 403 workload_not_production
// for a non-production runtime, else 401 naming the token required.
func Refuse(w http.ResponseWriter, err error, required string) {
	if errors.Is(err, ErrNotProduction) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeWorkloadNotProduction, "Production workload required",
			"only a Data API runtime serving a production deployment reports to the platform"))
		return
	}
	api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", required))
}

// Bearer is the request's single bearer token, or "".
func Bearer(r *http.Request) string {
	if len(r.Header.Values("Authorization")) != 1 {
		return ""
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return token
}
