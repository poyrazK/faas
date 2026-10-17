package mcpmanagement

import "github.com/onebox-faas/faas/pkg/api"

type requestWakeView struct {
	Status                     string            `json:"status"`
	Basis                      string            `json:"basis"`
	AssociationScope           string            `json:"association_scope"`
	WakeID                     string            `json:"wake_id,omitempty"`
	RepresentsMultipleRequests bool              `json:"represents_multiple_requests"`
	TimelineAvailability       string            `json:"timeline_availability"`
	TimelineTool               string            `json:"timeline_tool,omitempty"`
	TimelineArguments          *wakeTimelineArgs `json:"timeline_arguments,omitempty"`
	Limitations                string            `json:"limitations"`
}

func (m *management) projectRequestWake(app string, r api.DebugTelemetryRequestItem) requestWakeView {
	v := requestWakeView{Status: "unavailable", Basis: "native_retained_request_wake_id", AssociationScope: "retained_row", TimelineAvailability: "not_checked",
		Limitations: "Recorded row association, not a per-request startup duration or causal claim. Event retention and access are independent. Missing IDs do not establish warm execution, failure or no wake. Timeline arguments describe a separate authorized read."}
	if r.ID == "" || r.EvidenceStatus == "request_id_only" || r.Count <= 0 {
		return v
	}
	v.RepresentsMultipleRequests = r.Count > 1
	v.Status = "not_reported"
	if r.WakeID == "" {
		return v
	}
	args, err := m.wakeTimelineSelection(wakeTimelineArgs{App: app, WakeID: r.WakeID})
	if err != nil || m.clean(app) != app {
		v.Status = "invalid_native_wake"
		return v
	}
	v.Status, v.WakeID = "observed", r.WakeID
	v.TimelineTool, v.TimelineArguments = "gregale_get_wake_timeline", &args
	return v
}

func linkRequestWakeRecommendation(e requestEvidenceView, v requestRecommendationView) requestRecommendationView {
	wake := e.Request.Wake
	if v.Action != "inspect_wake_timeline" || wake.Status != "observed" || wake.TimelineArguments == nil {
		return v
	}
	v.Tool, v.Arguments = wake.TimelineTool, wake.TimelineArguments
	v.EvidenceRefs = []string{"/request/wake"}
	v.Detail = "Inspect the recorded wake association with the explicit timeline arguments when further startup evidence is relevant. Event availability is not checked; the independent read rechecks diagnostic consent, app identity and native entitlement. A shared wake does not establish per-request phase durations or root cause. No follow-up executes automatically."
	return v
}

const requestWakeGuidance = "Inspect request.wake (or each discovery row's wake) as a native_retained_request_wake_id association with association_scope retained_row. Only status observed supplies an unchanged wake_id, timeline_tool gregale_get_wake_timeline and explicit timeline_arguments for the same app with a bounded native event limit. These arguments describe a separate read, not a permission grant or pagination cursor. timeline_availability not_checked applies even if reduced request timeline events are present: standalone event retention, entitlement/MFA and consent are independent and checked on that call. unavailable means no usable retained request row; not_reported means no native wake ID; invalid_native_wake withholds unsafe identifiers without rewriting them or discarding other request evidence. None establishes warm execution, no wake or failure. Do not derive a wake ID from request/trace/instance IDs, timestamps, cold_boot flags or event display labels. Preserve represents_multiple_requests and native represented counts: a retained association is not an exact individual execution, complete wake membership, per-request startup duration or causal proof. Do not infer cold_boot from a wake ID or invent a wake association from cold_boot. Pass only the returned app/wake_id/limit for a relevant explicit stage read; no request lookback, receipt-time cutoff, historical until or cursor is inferred. Never automatically enrich every discovery row or fetch timelines during request/prompt projection. Unknown/missing/invalid associations produce no timeline arguments. A supported inspect_wake_timeline recommendation can cite /request/wake and expose explicit tool arguments; the existing generic timeline/correlation suggestion remains when no safe association is available. Independent reads preserve the native timeline's explicit keyset continuation or legacy timestamp_only_not_lossless compatibility status, and its timing-boundary limits. No extra environment, log or write access, probes, replay, collection or mutations are granted."
