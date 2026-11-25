package gateway

import (
	"context"
	"net/http"
)

type billingRouteKey struct{}

// withBillingRoute stores the bounded route label of a consumer- or
// tenant-source-attributed request. apid keeps that subject's billable
// units per label so rate cards can weight routes (ADR-952, ADR-1310). The
// overflow label is kept too and is discarded by apid, so its requests
// count at weight 1.
func withBillingRoute(r *http.Request, route string) *http.Request {
	if r == nil || route == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), billingRouteKey{}, route))
}

type billingRouteCandidateKey struct{}

// withBillingRouteCandidate keeps a request's route label until a JWT
// authorization rule can attribute it to a platform tenant, which runs
// after edge rewrites change the path (ADR-1310).
func withBillingRouteCandidate(r *http.Request, route string) *http.Request {
	if r == nil || route == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), billingRouteCandidateKey{}, route))
}

// admitJWTTenantBillingRoute gives a request a JWT rule attributed to a
// tenant the billing route of its original path, so tenant route weights
// apply to it (ADR-1310).
func (h *Handler) admitJWTTenantBillingRoute(r *http.Request, appID string) *http.Request {
	auth := authenticatedFrom(r.Context())
	if auth.ConsumerID != "" || auth.PlatformTenantSurfaceID != "" || auth.PlatformTenantJWTAuthorizationRuleID == "" {
		return r
	}
	candidate, _ := r.Context().Value(billingRouteCandidateKey{}).(string)
	if candidate == "" {
		return r
	}
	return withBillingRoute(r, h.billingRouteSetFor(appID).admit(candidate))
}

func billingRouteFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	value, _ := r.Context().Value(billingRouteKey{}).(string)
	if value == otherRouteLabel {
		return ""
	}
	return value
}

// billingRouteSetFor returns the app's bounded billing label set. It is
// separate from the route-metrics sets so enabling or disabling metrics
// never changes which routes billing can tell apart.
func (h *Handler) billingRouteSetFor(appID string) *routeLabelSet {
	if v, ok := h.billingRouteSets.Load(appID); ok {
		return v.(*routeLabelSet)
	}
	actual, _ := h.billingRouteSets.LoadOrStore(appID, newRouteLabelSet())
	return actual.(*routeLabelSet)
}

type planRouteKey struct{}

// withPlanRoute keeps a consumer request's route label before the app's
// bounded billing label set can fold it into the overflow label. Plan
// route limits and plan unit weights look routes up by it, so filling the
// set with made-up paths cannot switch a cap off (ADR-20261123192254).
func withPlanRoute(r *http.Request, route string) *http.Request {
	if r == nil || route == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), planRouteKey{}, route))
}

// planRouteFrom is the route consumer plan admission uses: the label
// withPlanRoute kept, else the billing label.
func planRouteFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	if route, _ := r.Context().Value(planRouteKey{}).(string); route != "" {
		return route
	}
	return billingRouteFrom(r)
}
