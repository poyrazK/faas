package gateway

import (
	"context"
	"net/http"
)

type billingRouteKey struct{}

// withBillingRoute stores the bounded route label of a consumer-attributed
// request. apid keeps that consumer's billable units per label so rate
// cards can weight routes (ADR-952). The overflow label is kept too and is
// discarded by apid, so its requests count at weight 1.
func withBillingRoute(r *http.Request, route string) *http.Request {
	if r == nil || route == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), billingRouteKey{}, route))
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
