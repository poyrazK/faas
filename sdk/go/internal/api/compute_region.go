package api

import "strings"

// ValidComputeRegion accepts canonical operator-defined locality identifiers.
// The empty value preserves unconstrained placement for existing applications.
func ValidComputeRegion(region string) bool {
	if region == "" {
		return true
	}
	if len(region) > 63 || strings.HasPrefix(region, "-") || strings.HasSuffix(region, "-") {
		return false
	}
	for _, c := range region {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// ComputeRegionResponse describes registered compute locality, not a promise
// of spare capacity or regional control-plane independence.
type ComputeRegionResponse struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name"`
	AcceptingNewApps bool   `json:"accepting_new_apps"`
	Availability     string `json:"availability"`
	ObservedHealth   string `json:"observed_health"`
	RegisteredNodes  int    `json:"registered_nodes"`
	ActiveNodes      int    `json:"active_nodes"`
	EligibleNodes    int    `json:"eligible_nodes"`
	HealthyNodes     int    `json:"healthy_nodes"`
}

// UpdateComputeRegionRequest declares complete operator-controlled policy.
type UpdateComputeRegionRequest struct {
	DisplayName      string `json:"display_name"`
	AcceptingNewApps *bool  `json:"accepting_new_apps"`
}
