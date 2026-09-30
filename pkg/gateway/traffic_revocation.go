// adr: 375
package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

type trafficEnrollmentKey struct{}

// A lifetime fence owns all scopes acquired by this request, including later
// attempts. Cancellation does not release forwarding ownership. Registrations
// remain until the final handler cleanup, after its response-write guards stop.
type trafficEnrollment struct {
	mu       sync.Mutex
	registry *trafficrevocation.Registry
	cancel   context.CancelCauseFunc
	scopes   map[trafficrevocation.Scope]struct{}
	releases []func()
	closed   bool
}

func (h *Handler) WithTrafficRevocations(registry *trafficrevocation.Registry) *Handler {
	h.trafficRevocations = registry
	return h
}

func enrollTrafficScopes(w http.ResponseWriter, r *http.Request, registry *trafficrevocation.Registry, scopes ...trafficrevocation.Scope) bool {
	return enrollTrafficScopesWith(w, r, registry, func(err error) { writeTrafficRevocationError(w, r, err) }, scopes...)
}

func enrollPublicTrafficScopes(w http.ResponseWriter, r *http.Request, registry *trafficrevocation.Registry, app App) bool {
	return enrollTrafficScopesWith(w, r, registry, func(err error) {
		// Preserve known initial suspension/hold contracts. Active exchanges
		// use the generic revocation code, since their snapshot may be older.
		if errors.Is(err, trafficrevocation.ErrRevoked) {
			if app.AccountAbuseHeld {
				api.WriteProblem(w, api.ErrAccountAbuseHold())
				return
			}
			if app.AccountStatus == "suspended" || app.AccountStatus == "deleted_pending" {
				api.WriteProblem(w, api.ErrAccountSuspended())
				return
			}
		}
		writeTrafficRevocationError(w, r, err)
	}, trafficrevocation.Scope{Kind: "account", ID: app.AccountID}, trafficrevocation.Scope{Kind: "app", ID: app.ID})
}

func enrollTrafficScopesWith(w http.ResponseWriter, r *http.Request, registry *trafficrevocation.Registry, refuse func(error), scopes ...trafficrevocation.Scope) bool {
	if registry == nil { // Optional only for legacy in-process fixtures.
		return false
	}
	if handleForwardRequestCancellation(w, r, true) {
		return true
	}
	enrollment, _ := r.Context().Value(trafficEnrollmentKey{}).(*trafficEnrollment)
	if enrollment == nil {
		ctx, cancel := reqbudget.WithCancellationFence(r.Context())
		enrollment = &trafficEnrollment{registry: registry, cancel: cancel, scopes: make(map[trafficrevocation.Scope]struct{})}
		ctx = context.WithValue(ctx, trafficEnrollmentKey{}, enrollment)
		rememberBudgetCancel(r, ctx, enrollment.close)
	}
	if err := enrollment.add(r.Context(), scopes); err != nil {
		if cause := trafficRevocationCause(r.Context()); cause != nil {
			refuse(cause)
			return true
		}
		if r.Context().Err() != nil {
			return handleForwardRequestCancellation(w, r, true)
		}
		enrollment.cancel(err)
		refuse(err)
		return true
	}
	return handleForwardRequestCancellation(w, r, true)
}

func (e *trafficEnrollment) add(ctx context.Context, scopes []trafficrevocation.Scope) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return trafficrevocation.ErrUnavailable
	}
	fresh := make([]trafficrevocation.Scope, 0, len(scopes))
	seen := make(map[trafficrevocation.Scope]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope.ID == "" {
			return trafficrevocation.ErrUnavailable
		}
		if _, exists := e.scopes[scope]; exists {
			continue
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		fresh = append(fresh, scope)
	}
	if len(e.scopes)+len(fresh) > api.TrafficSecurityMaxRequestScopes {
		return &trafficrevocation.CapacityError{Resource: "request_scopes", Limit: api.TrafficSecurityMaxRequestScopes, Observed: len(e.scopes) + len(fresh)}
	}
	if len(fresh) == 0 {
		return ctx.Err()
	}
	release, err := e.registry.Admit(ctx, fresh, e.cancel)
	if err != nil {
		return err
	}
	for _, scope := range fresh {
		e.scopes[scope] = struct{}{}
	}
	e.releases = append(e.releases, release)
	return nil
}

func (e *trafficEnrollment) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	for _, release := range e.releases {
		release()
	}
	e.cancel(nil)
}

func trafficRevocationCause(ctx context.Context) error {
	cause := reqbudget.CancellationFenceCause(ctx)
	if errors.Is(cause, trafficrevocation.ErrRevoked) || errors.Is(cause, trafficrevocation.ErrUnavailable) || errors.Is(cause, trafficrevocation.ErrCapacity) {
		return cause
	}
	return nil
}

func writeTrafficRevocationError(w http.ResponseWriter, r *http.Request, err error) {
	// The lifetime is canceled; error delivery gets a separate short allowance.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), api.RequestBudgetErrorWriteTimeout)
	defer cancel()
	defer guardResponseWrites(ctx, w)() //nolint:contextcheck // bounded error delivery after lifetime cancellation.
	problem := api.NewProblem(http.StatusServiceUnavailable, api.CodeTrafficRevocationUnavailable,
		"Traffic security verification unavailable", "the platform could not verify the request security generation; retry shortly")
	if errors.Is(err, trafficrevocation.ErrRevoked) {
		problem = api.NewProblem(http.StatusForbidden, api.CodeTrafficRevoked,
			"Traffic revoked", "the account, app or deployment security generation changed; this request can no longer continue")
	} else {
		w.Header().Set("Retry-After", "1")
	}
	var capacity *trafficrevocation.CapacityError
	if errors.As(err, &capacity) {
		problem = problem.WithLimit(int64(capacity.Limit), int64(capacity.Observed)).WithDocs("https://gregale.dev/status")
	}
	if rid := requestIDFrom(r); rid != "" {
		w.Header().Set(api.RequestIDHeader, rid)
	}
	w.Header().Set(api.ErrorCodeHeader, problem.Code)
	w.Header().Set("Cache-Control", "no-store")
	api.WriteProblem(w, problem)
}
