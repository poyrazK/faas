// Package monetization names the customer-facing surface of API monetization
// (ADR-20261018090210): pricing a customer's own consumers and platform
// tenants, their statements, invoice exports, billing runs and the controls
// built on them. While the api_monetization_enabled runtime setting is off,
// apid answers its routes like unregistered ones, leaves them out of the
// served OpenAPI document and does not offer its webhook events. It imports
// only the standard library so the spec server can use it.
package monetization

import (
	"path"
	"slices"
	"strings"
)

// routes lists every API monetization path. "{}" matches one non-empty path
// segment, and a pattern also covers every path below it. All methods of a
// listed path belong to the feature, so method probes are hidden too.
var routes = []string{
	"/v1/apps/{}/rate-cards",
	"/v1/apps/{}/consumer-plans",
	"/v1/apps/{}/consumer-billing-policy",
	"/v1/apps/{}/consumer-overdue-holds",
	"/v1/apps/{}/consumers/{}/usage/quote",
	"/v1/apps/{}/consumers/{}/usage-alerts",
	"/v1/apps/{}/consumers/{}/usage-completeness",
	"/v1/apps/{}/consumers/{}/usage-statements",
	"/v1/apps/{}/consumers/{}/plan-assignments",
	"/v1/apps/{}/consumers/{}/credits",
	"/v1/apps/{}/consumers/{}/spend-cap",
	"/v1/apps/{}/consumers/{}/trial",
	"/v1/account/billing-connections",
	"/v1/account/billing-run-schedule",
	"/v1/account/billing-runs",
	"/v1/account/statement-invoices",
	"/v1/account/platform-tenant-plans",
	"/v1/account/platform-tenant-billing-policy",
	"/v1/account/platform-tenant-overdue-holds",
	"/v1/account/platform-tenants/{}/rate-cards",
	"/v1/account/platform-tenants/{}/usage-statements",
	"/v1/account/platform-tenants/{}/plan-assignments",
	"/v1/account/platform-tenants/{}/trial",
	"/v1/account/platform-tenants/{}/credits",
	"/v1/account/platform-tenants/{}/spend-cap",
	"/v1/consumer-billing",
	"/v1/platform-tenant-self/usage-statements",
}

// Path reports whether a request path, or an OpenAPI path template, belongs
// to API monetization. The path is cleaned first, so dot segments and
// repeated slashes cannot step around the gate.
func Path(p string) bool {
	got := strings.Split(strings.TrimPrefix(path.Clean("/"+p), "/"), "/")
	for _, route := range routes {
		want := strings.Split(strings.TrimPrefix(route, "/"), "/")
		if len(got) < len(want) {
			continue
		}
		match := true
		for i, segment := range want {
			if got[i] == "" || segment != "{}" && segment != got[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// WebhookEvents are the app and platform tenant webhook events that only
// API monetization emits.
var WebhookEvents = []string{
	"usage_statement.finalized",
	"consumer.usage_threshold",
	"platform_tenant.statement.finalized",
}

// Schemas are the OpenAPI components only API monetization's webhook events
// use. No path references them, so the served spec removes them by name.
var Schemas = []string{
	"APIConsumerUsageThresholdWebhookPayload",
	"APIConsumerUsageStatementFinalizedWebhookPayload",
	"PlatformTenantStatementFinalizedWebhookPayload",
}

// WebhookEvent reports whether event is one of WebhookEvents.
func WebhookEvent(event string) bool { return slices.Contains(WebhookEvents, event) }

// VisibleWebhookEvents returns events without the monetization ones unless
// monetization is enabled.
func VisibleWebhookEvents(events []string, enabled bool) []string {
	if enabled {
		return append([]string(nil), events...)
	}
	visible := make([]string, 0, len(events))
	for _, event := range events {
		if !WebhookEvent(event) {
			visible = append(visible, event)
		}
	}
	return visible
}
