package mcpmanagement

import "github.com/onebox-faas/faas/pkg/api"

type requestGuestView struct {
	Status                     string `json:"status"`
	Runtime                    string `json:"runtime"`
	Outcome                    string `json:"outcome"`
	ErrorClass                 string `json:"error_class"`
	DurationMS                 *int   `json:"duration_ms,omitempty"`
	DurationPrecision          string `json:"duration_precision"`
	RepresentsMultipleRequests bool   `json:"represents_multiple_requests"`
	Limitations                string `json:"limitations"`
}

func projectRequestGuest(r api.DebugTelemetryRequestItem) requestGuestView {
	v := requestGuestView{Status: "unavailable", Runtime: "unknown", Outcome: "unknown", ErrorClass: "not_reported", DurationPrecision: "native_retained_may_be_bucketed",
		Limitations: "Runner execution evidence retained with this request row. Duration may be a telemetry bucket upper bound even for count one, not exact individual execution time or full request latency. A collapsed row can represent multiple requests. Runner outcome is independent of the HTTP outcome and is not proof of root cause or application health. Missing evidence, unknown categories or an absent error class do not establish success. No payload, error message, stack trace, CPU/RSS data or additional permissions are exposed."}
	if r.ID == "" || r.EvidenceStatus == "request_id_only" || r.Count <= 0 {
		return v
	}
	v.RepresentsMultipleRequests = r.Count > 1
	if r.Guest == nil {
		return v
	}
	g := r.Guest
	switch g.Runtime {
	case "node22", "node24", "python312", "python313", "go124", "binary":
		v.Runtime = g.Runtime
	}
	switch g.Outcome {
	case "ok", "http_error", "handler_error", "timeout", "canceled", "missing":
		v.Outcome = g.Outcome
	}
	switch g.ErrorClass {
	case "":
	case "http_5xx", "handler_exec", "handler_protocol", "timeout", "canceled":
		v.ErrorClass = g.ErrorClass
	default:
		v.ErrorClass = "unknown"
	}
	if g.Outcome == "missing" || g.Outcome == "" {
		return v
	}
	if g.DurationMS < 0 || g.DurationMS > api.MCPRequestGuestDurationMaxMS || !consistentGuestOutcome(v.Outcome, v.ErrorClass) {
		v.Status = "invalid_native_guest"
		return v
	}
	v.DurationMS, v.Status = &g.DurationMS, "observed"
	if v.Runtime == "unknown" || v.Outcome == "unknown" || v.ErrorClass == "unknown" {
		v.Status = "partial"
	}
	return v
}

func consistentGuestOutcome(outcome, class string) bool {
	if class == "not_reported" || class == "unknown" || outcome == "unknown" {
		return true // Missing/unknown metadata is not evidence of a contradiction.
	}
	switch outcome {
	case "http_error":
		return class == "http_5xx"
	case "handler_error":
		return class == "handler_exec" || class == "handler_protocol"
	case "timeout":
		return class == "timeout"
	case "canceled":
		return class == "canceled"
	default:
		return false
	}
}

const requestGuestGuidance = "Inspect the returned guest execution summary separately from HTTP status. Runtime/outcome/error-class values are closed categories; unknown, unavailable and invalid_native_guest remain uncertain. duration_precision native_retained_may_be_bucketed means the retained duration can be a bucket upper bound even for count one; it is not exact execution time, full request latency or a quantity to subtract from request/path duration. Preserve represented counts and do not extrapolate a collapsed row into independent executions. A guest_failure finding must reference the exposed /request/guest summary with a supported failed runner outcome; missing or successful runner evidence cannot establish failure. An absent error class does not establish success. No error text, stack trace, payload, resource measurements or additional logs are granted by this summary or an investigation suggestion. Do not execute suggestions automatically."
