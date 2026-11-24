package gateway

import (
	"context"
	"net/http"
	"sync/atomic"

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

// gatewayRefusalUnbillable reports whether a request ended in a 4xx that the
// guest never started answering: the gateway refused it (gateway-enforced
// authentication, an edge rule, a route rate limit or quota, a malformed
// request) after the consumer or tenant was already identified. The customer's
// application did not run, so the refusal carries zero billable units, like
// the consumer-plan and tenant-budget refusals that suppress usage explicitly
// (ADR-1306). A 4xx the guest itself returned is still billable.
// ADR-20261107170000.
func gatewayRefusalUnbillable(r *http.Request, status int) bool {
	if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
		return false
	}
	_, guestResponded := FirstByteFrom(r)
	return !guestResponded
}

// usageUnbillable is the billing rule: a response the guest never started
// answering is not billed, whatever its error status. It is deliberately
// separate from platformErrorForSLO, which counts only 5xx: a gateway refusal
// is the platform working, not failing. MCP protocol overhead an MCP resource
// policy admitted is not billed either (ADR-20261123192254).
func usageUnbillable(r *http.Request, status int) bool {
	return platformFailureUnbillable(r, status) || gatewayRefusalUnbillable(r, status) || mcpProtocolOverheadFrom(r)
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

// capacityRefusalForSLO reports whether a response counts in
// gateway_capacity_refusals_total: a 429 the gateway answered, before the
// guest sent any byte, because the app had no capacity (markCapacityRefused).
// The app's visitors were turned away, so customer SLO availability and
// outage detection count it as a failed request. The platform availability
// SLO and the status page do not: the app reached its own plan limits
// (ADR-20261121190000).
func capacityRefusalForSLO(r *http.Request, status int) bool {
	if status != http.StatusTooManyRequests || !capacityRefusedFrom(r) {
		return false
	}
	_, guestResponded := FirstByteFrom(r)
	return !guestResponded
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

type capacityRefusalKey struct{}

// withCapacityRefusalMark plants the flag markCapacityRefused sets. ServeHTTP
// installs it first, before the wake path can refuse the request.
func withCapacityRefusalMark(ctx context.Context) context.Context {
	return context.WithValue(ctx, capacityRefusalKey{}, new(atomic.Bool))
}

// markCapacityRefused records that the gateway refused the request for lack of
// app capacity: the plan's concurrency was reached, the drop overflow mode
// shed it, the concurrency queue was full or the node refused it. Observe
// counts it in gateway_capacity_refusals_total, which customer SLOs, error
// rates and outage detection read as failed requests (Observe audit
// 2026-11-21, findings 8 and 15; ADR-20261121190000). A request without the
// flag is ignored.
func markCapacityRefused(r *http.Request) {
	if r == nil {
		return
	}
	if flag, ok := r.Context().Value(capacityRefusalKey{}).(*atomic.Bool); ok && flag != nil {
		flag.Store(true)
	}
}

// capacityRefusedFrom reports markCapacityRefused for r.
func capacityRefusedFrom(r *http.Request) bool {
	if r == nil {
		return false
	}
	flag, ok := r.Context().Value(capacityRefusalKey{}).(*atomic.Bool)
	return ok && flag != nil && flag.Load()
}
