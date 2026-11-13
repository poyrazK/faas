package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

// FunctionSendStore is what an async function send needs: the async route
// enqueue path plus the target's plan.
type FunctionSendStore interface {
	AsyncRouteInvocationStore
	AccountByID(context.Context, string) (state.Account, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	AppBySlug(context.Context, string) (state.App, error)
}

// functionSendRuleID labels sends in async-route metrics; no edge rule exists.
const functionSendRuleID = "function-send"

// NewFunctionSendAdmitter admits a service call marked X-Gregale-Invoke: async
// (an SDK functions client's send(), ADR-20261111150000 §3) into the durable
// async invocation path instead of forwarding it: it is enqueued against the
// target app exactly like an async route (plan gate, payload cap, retries,
// dead letters, `gregale invocation get`) and answers 202 with the invocation
// id. It runs only after the service proxy authorized the caller, so the
// caller's VM identity is the authority and no API key is needed in the guest.
// Calls without the header are not touched.
func NewFunctionSendAdmitter(store FunctionSendStore) ServiceProxyAsyncAdmitter {
	return func(w http.ResponseWriter, r *http.Request, caller ServiceCaller, target ServiceTarget, path string) bool {
		if r.Header.Get(api.FunctionInvokeHeader) != api.FunctionInvokeAsync {
			return false
		}
		if caller.RehearsalRunID != "" {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Async send unavailable", "rehearsal apps cannot send async function calls"))
			return true
		}
		// The SDK transport carries the caller's release on every managed call,
		// so a release pin is not refused: the send runs on the callee release
		// active in the caller's environment when it is enqueued (the enqueue
		// pins that version). An explicit revision override or an upgrade has
		// no meaning for durable work.
		_, override, _ := serviceDeploymentOverrideFromRequest(r)
		if override || isUpgradeRequest(r) {
			api.WriteProblem(w, api.ErrValidation("an async send cannot pin a revision or upgrade the connection; send it without those"))
			return true
		}
		app, err := store.AppByID(r.Context(), target.AppID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("async send target unavailable"))
			return true
		}
		account, err := store.AccountByID(r.Context(), app.AccountID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("async send account unavailable"))
			return true
		}
		// The send lands in the caller's environment, as a synchronous call
		// routes to the callee's release there: a staging caller enqueues
		// against the callee's staging deployment. The caller's deployment comes
		// from node-local instance identity, never a guest header; without one
		// the call is a legacy production caller.
		scope := ""
		if caller.DeploymentID != "" {
			deployment, err := store.DeploymentByID(r.Context(), caller.DeploymentID)
			if err != nil {
				api.WriteProblem(w, api.ErrCapacity("async send caller environment unavailable"))
				return true
			}
			scope = deployment.Scope
		}
		onComplete, problem := functionSendCompletionTarget(r, store, caller, app)
		if problem != nil {
			api.WriteProblem(w, problem)
			return true
		}
		request := r.Clone(r.Context())
		request.Header = r.Header.Clone()
		// The stored request replays straight to the callee; the marker has done
		// its job here. X-Gregale-Function-Call stays, so the callee still checks
		// the input against its schema when the invocation runs.
		request.Header.Del(api.FunctionInvokeHeader)
		request.Header.Del(api.FunctionOnCompleteHeader)
		// The caller's release and request deadline belong to the caller's
		// request, not to work that runs later under its own deadline.
		request.Header.Del(api.ReleaseHeader)
		request.Header.Del(trafficdeadline.Header)
		u := *r.URL
		u.Path, u.RawPath = path, ""
		request.URL = &u
		h := Handler{asyncRoutes: functionSendEnqueuer{store: store, sender: caller.AppID, onComplete: onComplete}}
		rule := &EdgeRuleAsyncResolved{ID: functionSendRuleID, AccountID: app.AccountID, AppID: app.ID}
		return h.applyEdgeRuleAsync(w, request, App{
			ID: app.ID, AccountID: app.AccountID, Plan: account.Plan, Scope: scope,
			RequestInvocationsEnabled: app.AcceptsRequestInvocations(),
		}, rule)
	}
}

// functionSendEnqueuer stamps the send's sender and completion target onto
// the async route request.
type functionSendEnqueuer struct {
	store              AsyncRouteInvocationStore
	sender, onComplete string
}

func (e functionSendEnqueuer) EnqueueAsyncRoute(ctx context.Context, r AsyncRouteRequest) (AsyncRouteAccepted, error) {
	r.SentByAppID, r.OnCompleteAppID = e.sender, e.onComplete
	return EnqueueAsyncRoute(ctx, e.store, r)
}

// functionSendCompletionTarget resolves X-Gregale-On-Complete to an app id:
// the caller itself for "self", else a name in the caller's own
// function_callables. The completion goes to an app the caller may already
// call, in its account; anything else is refused before the send is queued.
func functionSendCompletionTarget(r *http.Request, store FunctionSendStore, caller ServiceCaller, target state.App) (string, *api.Problem) {
	name := r.Header.Get(api.FunctionOnCompleteHeader)
	if name == "" {
		return "", nil
	}
	if caller.AppID == "" {
		return "", api.ErrValidation("onComplete needs a calling function")
	}
	if name == api.FunctionOnCompleteSelf {
		return caller.AppID, nil
	}
	callerApp, err := store.AppByID(r.Context(), caller.AppID)
	if err != nil {
		return "", api.ErrCapacity("async send caller unavailable")
	}
	slug, ok := callerApp.Manifest.FunctionCallables[name]
	if !ok {
		return "", api.ErrValidation("onComplete names " + name + ", which this function cannot call; use a name from its functions or \"self\"")
	}
	app, err := store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != callerApp.AccountID || app.AccountID != target.AccountID {
		return "", api.ErrValidation("onComplete names " + name + ", which is not available")
	}
	return app.ID, nil
}

// ChainServiceProxyAsyncAdmitters runs admitters in order; the first that
// handles the call wins. Nil admitters are skipped.
func ChainServiceProxyAsyncAdmitters(admitters ...ServiceProxyAsyncAdmitter) ServiceProxyAsyncAdmitter {
	return func(w http.ResponseWriter, r *http.Request, caller ServiceCaller, target ServiceTarget, path string) bool {
		for _, admit := range admitters {
			if admit != nil && admit(w, r, caller, target, path) {
				return true
			}
		}
		return false
	}
}
