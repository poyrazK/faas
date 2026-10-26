// Package realtimepreview names the API routes and fields of the realtime
// preview (ADR-20261019102657). apid answers the routes like unregistered
// ones for accounts outside FAAS_REALTIME_PREVIEW_ACCOUNTS, and the served
// OpenAPI document leaves the routes and fields out until every account sees
// the preview. It imports only the standard library so the spec server can
// use it.
package realtimepreview

import (
	"path"
	"slices"
	"strings"
)

// routes lists every preview route as "METHOD template", the patterns apid
// registers. No path mixes preview and released methods.
var routes = []string{
	"GET /v1/account/realtime-history-usage",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/acknowledgement",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/circuit",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/circuit",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claim-rate",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claim-rate",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/batch",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/settle",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/settle/batch",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/revoke",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/rotate",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letter-retention",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letter-retention",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/{sequence}/replay",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/export",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/preview",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/replay",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/drain",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/heartbeats",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/heartbeats",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/lag-alert",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/lag-alert",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/messages/{sequence}/history",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/metrics",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/pause",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/priority",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs/{job}/{action}",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs/dry-run",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/resume",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/resync",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish-batch",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/read-progress",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/read-progress",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages/{message_id}",
	"PATCH /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages/{message_id}",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}",
	"PATCH /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/history",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/pause",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/resume",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/retry",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/groups/{group}/{group_action}",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schemas/{event_type}/{version}",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schemas/{event_type}/{version}",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/occupancy",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/{message_id}",
	"PATCH /v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/{message_id}",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/read-progress",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/read-progress",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/revoke",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/rotate",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/push/deliveries",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/push/devices",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/push/devices/{device}",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/push/devices/{device}",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}/timeline",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/push/preferences",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/push/preferences",
	"GET /v1/apps/{slug}/realtime/endpoints/{id}/push/providers",
	"PUT /v1/apps/{slug}/realtime/endpoints/{id}/push/providers/{provider}",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/tickets",
	"POST /v1/apps/{slug}/realtime/endpoints/{id}/tickets",
	"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/tickets/{ticket}",
	"POST /v1/realtime/publishers/endpoints/{endpoint_id}/channels/{channel}/publish",
	"DELETE /v1/realtime/workers/groups/{group_id}",
	"GET /v1/realtime/workers/groups/{group_id}",
	"POST /v1/realtime/workers/groups/{group_id}/claims",
	"POST /v1/realtime/workers/groups/{group_id}/claims/batch",
	"POST /v1/realtime/workers/groups/{group_id}/claims/settle",
	"POST /v1/realtime/workers/groups/{group_id}/claims/settle/batch",
	"GET /v1/realtime/workers/groups/{group_id}/dead-letters",
	"POST /v1/realtime/workers/groups/{group_id}/dead-letters/{sequence}/replay",
	"POST /v1/realtime/workers/groups/{group_id}/drain",
	"POST /v1/realtime/workers/groups/{group_id}/heartbeats",
	"POST /v1/realtime/workers/groups/{group_id}/pause",
	"POST /v1/realtime/workers/groups/{group_id}/resume",
	"POST /v1/realtime/workers/groups/{group_id}/resync",
	"GET /v1/usage/realtime",
}

// Routes returns a copy of the preview routes.
func Routes() []string { return slices.Clone(routes) }

// Route reports whether method on an OpenAPI path template is a preview
// route.
func Route(method, template string) bool {
	return slices.Contains(routes, strings.ToUpper(method)+" "+template)
}

// Path reports whether a request path matches a preview route's template
// with any method, so a method probe can be hidden too. The path is cleaned
// first, so dot segments and repeated slashes cannot step around the gate.
func Path(p string) bool {
	got := strings.Split(strings.TrimPrefix(path.Clean("/"+p), "/"), "/")
	for _, route := range routes {
		_, template, _ := strings.Cut(route, " ")
		if segmentsMatch(strings.Split(strings.TrimPrefix(template, "/"), "/"), got) {
			return true
		}
	}
	return false
}

func segmentsMatch(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for i, segment := range want {
		if got[i] == "" || !strings.HasPrefix(segment, "{") && segment != got[i] {
			return false
		}
	}
	return true
}

// Fields are, per schema, the properties of released schemas that only the
// preview accepts or returns.
var Fields = map[string][]string{
	"CreateManagedRealtimeEndpointRequest": {"slug", "channel_rules"},
	"UpdateManagedRealtimeEndpointRequest": {"slug", "channel_rules"},
	"ManagedRealtimeEndpointResponse":      {"slug", "channel_rules"},
	"ManagedRealtimeMessageRequest":        {"ttl_seconds", "expected_sequence", "metadata"},
}

// Components are the preview's components that no path references, as
// "kind/name": webhook payloads, export formats and the security schemes of
// worker and publisher credentials.
var Components = []string{
	"schemas/RealtimeReplayJobWebhookPayload",
	"schemas/RealtimeScheduleCompletionWebhookPayload",
	"schemas/RealtimeDeadLetterExportManifest",
	"schemas/RealtimeDeadLetterExportRecord",
	"securitySchemes/realtimePublisherCredential",
	"securitySchemes/realtimeWorkerCredential",
}

// WebhookEvents are the app webhook events only the preview emits.
var WebhookEvents = []string{
	"realtime.replay_job.completed",
	"realtime.replay_job.cancelled",
	"realtime.replay_job.processing_failed",
	"realtime.channel.acknowledged",
	"realtime.consumer_group.dead_lettered",
	"realtime.consumer_group.dead_letter_expiring",
	"realtime.consumer_group.delivery_expired",
	"realtime.consumer_group.worker_lost",
	"realtime.consumer_group.worker_recovered",
	"realtime.consumer_group.circuit_opened",
	"realtime.consumer_group.circuit_half_open",
	"realtime.consumer_group.circuit_closed",
	"realtime.consumer_group.lag_exceeded",
	"realtime.consumer_group.lag_recovered",
	"realtime.schedule.published",
	"realtime.schedule.failed",
	"realtime.schedule.skipped",
	"realtime.notification.sent",
	"realtime.notification.failed",
	"realtime.notification.expired",
	"realtime.notification.cancelled",
	"realtime.notification.superseded",
	"realtime.message.read",
	"realtime.inbox.acknowledged",
	"realtime.inbox.gap",
	"realtime.inbox.fallback_required",
}

// WebhookEvent reports whether event is one of WebhookEvents.
func WebhookEvent(event string) bool { return slices.Contains(WebhookEvents, event) }

// VisibleWebhookEvents returns events without the preview ones unless the
// account sees the preview.
func VisibleWebhookEvents(events []string, visible bool) []string {
	if visible {
		return slices.Clone(events)
	}
	return slices.DeleteFunc(slices.Clone(events), WebhookEvent)
}

// RetainedDeliveryRoute is the released publish route whose delivery query
// value RetainedDelivery only the preview accepts.
const (
	RetainedDeliveryRoute = "POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish"
	RetainedDelivery      = "retained"
)
