package api

// ServiceWakeAheadResponse is an app's ADR-956 wake-ahead opt-in. When
// enabled, a cold wake of this app also starts restoring the services it is
// measured to call soon after it wakes.
type ServiceWakeAheadResponse struct {
	Slug      string `json:"slug"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// SetServiceWakeAheadRequest is the PUT body for an app's wake-ahead opt-in.
type SetServiceWakeAheadRequest struct {
	Enabled *bool `json:"enabled"`
}
