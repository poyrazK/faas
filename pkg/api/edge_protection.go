package api

// EdgeProtectionResponse summarizes what the edge rejected for one app over
// a range: GET /v1/apps/{slug}/edge-protection. It reads the per-app gateway
// counters behind the edge security alert presets, so the numbers match what
// those alerts evaluate. Counts are rounded increases over the range; on a
// Prometheus failure Source starts with "degraded:" and counts are zero.
type EdgeProtectionResponse struct {
	AppID  string `json:"app_id"`
	Range  string `json:"range"`
	Source string `json:"source"`
	AsOf   string `json:"as_of"`
	// PreAuth counts pre-auth source-limit decisions. WouldBlock is observe
	// mode; Blocked was enforced. Route-level decisions are included.
	PreAuth EdgeProtectionPreAuth `json:"pre_auth"`
	// ValidationFailures counts kind=validate mismatches by validate_mode.
	ValidationFailures []EdgeProtectionCount `json:"validation_failures"`
	// Rejections counts requests the other edge gates answered, by gate
	// (Name) and response status.
	Rejections []EdgeProtectionRejection `json:"rejections"`
	// WAF counts kind=waf inspections. Requests a block rule answered with
	// 403 also appear in Rejections under gate "waf".
	WAF EdgeProtectionWAF `json:"waf"`
}

// EdgeProtectionWAF is the kind=waf part of an EdgeProtectionResponse.
// Inspected counts requests the OWASP CRS scored (Detected is the subset
// at or above the rule's anomaly threshold). NotInspected counts matched
// requests that were skipped to protect the node: above the app's
// inspection budget, behind a full queue, or failed. Categories counts
// detections by CRS attack category (one detection may count several);
// TopRules lists the CRS rule IDs that scored most, largest first, for
// tuning exclude_rule_ids. Warned, Blocked and InlineSkipped count the
// in-path header/URI checks of warn and block rules (ADR-831 amendment 4);
// InlineSkipped requests passed unchecked because the app's inline budget or
// the node's check slots were exhausted.
type EdgeProtectionWAF struct {
	Inspected     int64                 `json:"inspected"`
	Detected      int64                 `json:"detected"`
	NotInspected  int64                 `json:"not_inspected"`
	Warned        int64                 `json:"warned"`
	Blocked       int64                 `json:"blocked"`
	InlineSkipped int64                 `json:"inline_skipped"`
	Categories    []EdgeProtectionCount `json:"categories"`
	TopRules      []EdgeProtectionCount `json:"top_rules"`
}

// EdgeProtectionWAFTopRules bounds EdgeProtectionWAF.TopRules.
const EdgeProtectionWAFTopRules = 10

// NewEdgeProtectionWAF returns an empty section whose lists encode as [].
func NewEdgeProtectionWAF() EdgeProtectionWAF {
	return EdgeProtectionWAF{Categories: []EdgeProtectionCount{}, TopRules: []EdgeProtectionCount{}}
}

// EdgeProtectionPreAuth is the pre-auth part of an EdgeProtectionResponse.
type EdgeProtectionPreAuth struct {
	Blocked    int64 `json:"blocked"`
	WouldBlock int64 `json:"would_block"`
}

// EdgeProtectionCount is one labelled count.
type EdgeProtectionCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// EdgeProtectionRejection is one (gate, status) count.
type EdgeProtectionRejection struct {
	Gate   string `json:"gate"`
	Status string `json:"status"`
	Count  int64  `json:"count"`
}
