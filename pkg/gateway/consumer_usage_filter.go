package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

// platformFailureUnbillable reports whether a request ended in a 5xx that the
// guest never started answering: wake/capacity failures, queue timeouts,
// unreachable or vanished targets, and other gateway-authored errors. The
// first-byte recorder is stamped only after upstream response headers arrive
// (proxy and bridge paths alike), so an absent stamp proves the platform, not
// the customer's application, produced the failure. Such requests remain
// visible as request/error counts but carry zero billable units. A 5xx the
// guest itself returned is still billable.
func platformFailureUnbillable(r *http.Request, status int) bool {
	if status < http.StatusInternalServerError {
		return false
	}
	_, guestResponded := FirstByteFrom(r)
	return !guestResponded
}

// platformErrorForSLO reports whether a response counts in
// gateway_platform_errors_total, which feeds the public availability SLO
// (FaasApiAvailabilityLow, the status page). It is platformFailureUnbillable
// minus a request whose budget expired while the guest was handling it: the
// customer is told their app was slow, and one slow customer endpoint must not
// page on-call as a platform outage (Hunt 11 H11-34). Billing still follows
// platformFailureUnbillable.
func platformErrorForSLO(r *http.Request, status int) bool {
	return platformFailureUnbillable(r, status) && !appBudgetExpiredFrom(r)
}

// unattributedUsage reports whether a consumer-usage event carries nothing any
// reader uses (ADR-234 amendment). That means no consumer, no platform tenant,
// no request-audit evidence, and no discovered route.
//
// Such an event only incremented the app's __anonymous__ row in
// api_consumer_usage_minutes. That row is never read: consumer usage, rate
// cards, and statements are listed per real consumer, and tenant statements
// read platform_tenant_usage_minutes. Yet each one cost apid a two-statement
// transaction per request on the control-plane Postgres. Anything with
// attribution or evidence is still journaled, so its delivery guarantees are
// unchanged.
func unattributedUsage(event usageoutbox.Event) bool {
	return event.ConsumerID == "" &&
		event.PlatformTenantID == "" &&
		event.PlatformTenantSurfaceID == "" &&
		event.PlatformTenantJWTAuthorizationRuleID == "" &&
		event.Audit == nil &&
		event.DiscoveredRoute == ""
}
