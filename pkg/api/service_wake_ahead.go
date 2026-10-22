package api

// ServiceWakeAheadResponse is an app's ADR-977 wake-ahead opt-in. When
// enabled, a cold wake of this app also starts restoring the services it is
// measured to call soon after it wakes, and until that evidence exists, the
// services it declares (ADR-1137).
type ServiceWakeAheadResponse struct {
	Slug      string                   `json:"slug"`
	Enabled   bool                     `json:"enabled"`
	UpdatedAt string                   `json:"updated_at,omitempty"`
	Learned   *ServiceWakeAheadLearned `json:"learned,omitempty"`
}

// Evidence sources for gateway-measured route feature data (ADR-1152).
const (
	RouteEvidenceSourceLive        = "live"
	RouteEvidenceSourceUnavailable = "unavailable"
)

// ServiceWakeAheadLearned is what the gateways measured for this caller app
// (ADR-1152): its top service edges and what wake-ahead did over the evidence
// window. It is the same evidence the gateways decide on, before or after
// the app opts in.
type ServiceWakeAheadLearned struct {
	Source      string                   `json:"source"`
	Window      string                   `json:"window"`
	CallerWakes int64                    `json:"caller_wakes"`
	Targets     []ServiceWakeAheadTarget `json:"targets"`
}

// ServiceWakeAheadTarget is one measured caller → target edge.
type ServiceWakeAheadTarget struct {
	AppID     string  `json:"app_id"`
	Slug      string  `json:"slug"`
	CallShare float64 `json:"call_share"`
	ColdShare float64 `json:"cold_share"`
	Qualifies bool    `json:"qualifies"`
	Started   int64   `json:"started"`
	Used      int64   `json:"used"`
	Unused    int64   `json:"unused"`
	Failed    int64   `json:"failed"`
}

// SetServiceWakeAheadRequest is the PUT body for an app's wake-ahead opt-in.
type SetServiceWakeAheadRequest struct {
	Enabled *bool `json:"enabled"`
}
