package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/queueleases"
	"github.com/onebox-faas/faas/pkg/capgrant"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// CodeBindingNotGranted is the refusal for a binding token whose caller app
// does not declare the grant the call needs (ADR-20261122191423).
const CodeBindingNotGranted = "binding_not_granted"

// bindingCall is an authorized binding-token call: the caller app and the
// grants it declared, and the target the route acts on.
type bindingCall struct {
	Caller     state.App
	InstanceID string
	Target     state.App
	Uses       capgrant.Uses
	View       capgrant.Target
}

type bindingCallKey struct{}

func bindingCallFrom(ctx context.Context) (bindingCall, bool) {
	c, ok := ctx.Value(bindingCallKey{}).(bindingCall)
	return c, ok
}

// bindingResource names the resource a route acts on, read from the request
// before the handler runs, or "" when only the target is checked or the
// handler checks it itself (queue names live in the body).
type bindingResource func(r *http.Request) string

// bindingRoute serves a producer route to both API keys and gregale:bindings
// workload tokens (ADR-20261122191423). A JWT-shaped bearer takes the binding
// path: the token must come from a live instance of the caller app running a
// production deployment, the target app in the path must be in the caller's
// resource account, the cross_app_bindings flag must be on for that account,
// and the caller's `uses:` must grant kind on the target (and the resource,
// when resource reads one). The handler then runs as the target's resource
// account. Every other credential takes the keyed chain unchanged.
func (s *server) bindingRoute(kind capgrant.Kind, resource bindingResource, keyed http.HandlerFunc, next accountHandler) http.HandlerFunc {
	workload := middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{Log: s.log}, s.apiAuthLimiter)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			call, acct, problem := s.authorizeBindingCall(r, kind, resource)
			if problem != nil {
				api.WriteProblem(w, problem)
				return
			}
			if !s.capabilityAllowedFor(r, acct) {
				writeMissingRoute(w, r)
				return
			}
			ctx := appaccess.WithBindingGrant(r.Context(), call.Target, acct)
			ctx = context.WithValue(ctx, bindingCallKey{}, call)
			r = r.WithContext(ctx)
			if key := r.Header.Get("Idempotency-Key"); key != "" {
				// Receipts of different caller apps must not replay each
				// other's responses under the shared resource account.
				r.Header = r.Header.Clone()
				r.Header.Set("Idempotency-Key", bindingIdempotencyKey(call.Caller.ID, key))
			}
			s.requireAPIRequestBudget(next)(w, r, acct)
		}))
	return func(w http.ResponseWriter, r *http.Request) {
		if queueleases.WorkloadBearer(r) {
			workload.ServeHTTP(w, r)
			return
		}
		keyed(w, r)
	}
}

// bindingIdempotencyKey scopes a caller's Idempotency-Key to the caller app.
// It is a fixed 72 bytes, so any key the routes accept (≤128 bytes) still
// fits their cap after scoping.
func bindingIdempotencyKey(callerID, key string) string {
	sum := sha256.Sum256([]byte(callerID + "\x00" + key))
	return "binding:" + hex.EncodeToString(sum[:])
}

// bindingRoutesDisabled answers a binding token while cross_app_bindings is
// off as a route that does not exist: a 401 would count against the shared
// per-IP auth-failure limiter, and SDKs on one node retrying a credential
// fetch would lock that node's other tenants out of apid.
func bindingRoutesDisabled() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such API route")
}

func bindingUnauthorized(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required",
		"a live gregale:bindings workload token from a production instance is required: "+detail)
}

// bindingCallerRefused answers a correctly signed token whose caller may not
// use bindings (a stage instance, a stopped one, a deleted app). It is 403,
// not 401: these callers retry on every SDK credential refresh, and a 401
// counts against the shared per-IP auth-failure limiter, which would lock
// the node's other tenants out of apid.
func bindingCallerRefused(detail string) *api.Problem {
	return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Binding not allowed",
		"a live gregale:bindings workload token from a production instance is required: "+detail)
}

// authorizeBindingCall returns the authorized call and the account the
// handler acts as, or the refusal.
func (s *server) authorizeBindingCall(r *http.Request, kind capgrant.Kind, resource bindingResource) (bindingCall, state.Account, *api.Problem) {
	caller, instanceID, callerAcct, problem := s.verifyBindingCaller(r)
	if problem != nil {
		return bindingCall{}, state.Account{}, problem
	}
	ctx := r.Context()
	missing := api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
	target, err := s.store.AppBySlug(ctx, r.PathValue("slug"))
	if errors.Is(err, state.ErrNotFound) || err == nil && target.Status == state.AppDeleted {
		return bindingCall{}, state.Account{}, missing
	}
	if err != nil {
		return bindingCall{}, state.Account{}, api.ErrInternal("could not load the app; retry shortly")
	}
	targetAcct, err := state.AppResourceAccount(ctx, s.store, target)
	if err != nil || targetAcct.ID != callerAcct.ID {
		// Another account's app answers as a missing one.
		return bindingCall{}, state.Account{}, missing
	}
	if organization := r.PathValue("organization"); organization != "" {
		org, err := s.store.OrgByID(ctx, target.OrgID)
		if err != nil || org.Personal || org.Slug != organization {
			return bindingCall{}, state.Account{}, missing
		}
	}
	if !s.platformFlagFor(platformflags.CrossAppBindings, appFlagSubject(targetAcct, target)) {
		return bindingCall{}, state.Account{}, bindingRoutesDisabled()
	}

	call := bindingCall{
		Caller:     caller,
		InstanceID: instanceID,
		Target:     target,
		View: capgrant.Target{
			Self:         target.ID == caller.ID,
			Slug:         target.Slug,
			WorkloadName: target.WorkloadName,
			SameProject:  caller.ProjectID != "" && caller.ProjectID == target.ProjectID,
		},
	}
	if caller.Manifest.Uses != nil {
		call.Uses = caller.Manifest.Uses.Normalized()
	}
	name := ""
	if resource != nil {
		name = resource(r)
	}
	if problem := call.permits(kind, name, resource != nil); problem != nil {
		return bindingCall{}, state.Account{}, problem
	}
	return call, targetAcct, nil
}

// verifyBindingCaller verifies a gregale:bindings token: it must come from a
// live instance of an existing app that runs a production deployment of an
// active resource account.
func (s *server) verifyBindingCaller(r *http.Request) (state.App, string, state.Account, *api.Problem) {
	verifier := s.workloadTrust()
	if verifier == nil {
		return state.App{}, "", state.Account{}, bindingRoutesDisabled()
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	claims, err := verifier.Verify(token, workloadidentity.BindingsAudience, time.Now())
	if err != nil {
		return state.App{}, "", state.Account{}, bindingUnauthorized("the token is invalid or expired")
	}
	ctx := r.Context()
	caller, err := s.store.AppByID(ctx, claims.AppID)
	if err != nil || caller.AccountID != claims.AccountID || caller.Status == state.AppDeleted {
		return state.App{}, "", state.Account{}, bindingCallerRefused("the token's app does not exist")
	}
	instance, err := s.store.InstanceByID(ctx, claims.InstanceID)
	if err != nil || instance.AppID != caller.ID || !state.State(instance.State).CountsForConcurrency() {
		return state.App{}, "", state.Account{}, bindingCallerRefused("the token's instance is not live")
	}
	if !state.ProductionDeploymentScope(claims.Environment) {
		return state.App{}, "", state.Account{}, bindingCallerRefused("stage and branch deployments cannot use binding tokens yet")
	}
	deployment, err := s.store.DeploymentByID(ctx, instance.DeploymentID)
	if err != nil || deployment.AppID != caller.ID || !state.ProductionDeploymentScope(deployment.Scope) {
		return state.App{}, "", state.Account{}, bindingCallerRefused("the token's instance does not run a production deployment")
	}
	callerAcct, err := state.AppResourceAccount(ctx, s.store, caller)
	if err != nil || !callerAcct.Active() {
		return state.App{}, "", state.Account{}, bindingCallerRefused("the caller's account is not active")
	}
	return caller, instance.ID, callerAcct, nil
}

// permits checks kind on the call's target. checkResource is false when the
// handler checks the resource itself after decoding the body.
func (c bindingCall) permits(kind capgrant.Kind, resource string, checkResource bool) *api.Problem {
	if !c.Uses.PermitsAny(kind, c.View) {
		return bindingNotGranted(c.Caller, kind, c.Target, "")
	}
	if !checkResource {
		return nil
	}
	var ok bool
	switch kind {
	case capgrant.KindRealtime:
		ok = c.Uses.PermitsRealtime(c.View, resource)
	case capgrant.KindQueues:
		ok = c.Uses.PermitsQueue(c.View, resource)
	case capgrant.KindDelayedTasks:
		ok = c.Uses.PermitsDelayedTasks(c.View)
	}
	if !ok {
		return bindingNotGranted(c.Caller, kind, c.Target, resource)
	}
	return nil
}

func bindingNotGranted(caller state.App, kind capgrant.Kind, target state.App, resource string) *api.Problem {
	targetName := target.Slug
	if target.ID == caller.ID {
		targetName = capgrant.Self
	} else if target.WorkloadName != "" && target.ProjectID == caller.ProjectID {
		targetName = target.WorkloadName
	}
	what := fmt.Sprintf("%s on %q", kind, targetName)
	if resource != "" {
		what = fmt.Sprintf("%s %q on %q", kind, resource, targetName)
	}
	service := caller.WorkloadName
	if service == "" {
		service = caller.Slug
	}
	return api.NewProblem(http.StatusForbidden, CodeBindingNotGranted, "Binding not granted",
		fmt.Sprintf("app %q does not declare %s; add it to project.services.%s.uses in gregale.yaml and run `gregale project apply`", caller.Slug, what, service))
}

// bindingQueueAllowed is the queue-name check for send routes, which read the
// name from the body: nil for API-key calls and granted binding calls.
func bindingQueueAllowed(r *http.Request, queue, environment string) *api.Problem {
	call, ok := bindingCallFrom(r.Context())
	if !ok {
		return nil
	}
	// Grants and binding tokens are production-only, so a binding call may
	// not pick a stage queue binding through the body's environment.
	if environment != "" && !state.ProductionDeploymentScope(environment) {
		return api.NewProblem(http.StatusForbidden, CodeBindingNotGranted, "Binding not granted",
			fmt.Sprintf("binding tokens send only to production queues; environment %q is not granted", environment))
	}
	return call.permits(capgrant.KindQueues, queue, true)
}

func realtimeChannelResource(r *http.Request) string { return r.PathValue("channel") }

func targetOnlyResource(*http.Request) string { return "" }
