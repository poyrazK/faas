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
