package agents

// Who issues and revokes callback URLs (ADR-20261030125501 §3, step 4). An
// account API key reaches every realtime endpoint of the account. A
// stateless agent's own instance presents a gregale:agents workload token
// instead, so the app holds no account key; it reaches only its own app's
// endpoints.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// CallbackCaller is the credential behind an issue or revoke request.
type CallbackCaller struct {
	Account state.Account
	// AppID is set for a workload token: only this app's endpoints are the
	// caller's.
	AppID string
}

// owns reports whether endpoint is the caller's.
func (c CallbackCaller) owns(endpoint state.ManagedRealtimeEndpoint) bool {
	return endpoint.AccountID == c.Account.ID && (c.AppID == "" || endpoint.AppID == c.AppID)
}

// WorkloadStore is the state WorkloadCaller reads; state.Store implements it.
type WorkloadStore interface {
	state.AppOwnerReader
	AppByID(ctx context.Context, id string) (state.App, error)
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
	DeploymentByID(ctx context.Context, id string) (state.Deployment, error)
}

// ErrWorkloadCaller is a workload token that names no live production
// instance of an active account's app.
var ErrWorkloadCaller = errors.New("agents: invalid workload caller")

// WorkloadCaller binds a gregale:agents token to a live instance of its app.
// The instance must run a production deployment: callback URLs publish into
// the production inbox, so a staging or preview deployment must not issue or
// revoke them (as for queue leases, ADR-1141 amendment 2026-10-27).
func WorkloadCaller(r *http.Request, verifier *workloadidentity.Verifier, store WorkloadStore, now time.Time) (CallbackCaller, error) {
	if verifier == nil {
		return CallbackCaller{}, ErrWorkloadCaller
	}
	c, err := verifier.Verify(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), workloadidentity.AgentsAudience, now)
	if err != nil || !state.ProductionDeploymentScope(c.Environment) {
		return CallbackCaller{}, ErrWorkloadCaller
	}
	ctx := r.Context()
	app, err := store.AppByID(ctx, c.AppID)
	if err != nil || app.AccountID != c.AccountID || app.Status == state.AppDeleted {
		return CallbackCaller{}, ErrWorkloadCaller
	}
	// The resource owner decides whether the app may run; endpoints and
	// tokens use the app's own account, its storage namespace.
	owner, err := state.AppResourceAccount(ctx, store, app)
	if err != nil || !owner.Active() {
		return CallbackCaller{}, ErrWorkloadCaller
	}
	acct, err := store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return CallbackCaller{}, ErrWorkloadCaller
	}
	instance, err := store.InstanceByID(ctx, c.InstanceID)
	if err != nil || instance.AppID != app.ID || !state.State(instance.State).CountsForConcurrency() {
		return CallbackCaller{}, ErrWorkloadCaller
	}
	deployment, err := store.DeploymentByID(ctx, instance.DeploymentID)
	if err != nil || deployment.AppID != app.ID || !state.ProductionDeploymentScope(deployment.Scope) {
		return CallbackCaller{}, ErrWorkloadCaller
	}
	return CallbackCaller{Account: acct, AppID: app.ID}, nil
}
