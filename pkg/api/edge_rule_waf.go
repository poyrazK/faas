package api

import (
	"fmt"
	"slices"
)

// kind=waf modes (ADR-831). Every mode samples requests off the request path
// with the full rule set and reports detections. Warn and block also check
// headers and URI in-path with a smaller rule set at paranoia level 1
// (amendment 4): warn tags a detected request's response with X-WAF-Warning,
// block answers it with 403. Request bodies are never blocked.
const (
	EdgeWAFModeObserve = "observe"
	EdgeWAFModeWarn    = "warn"
	EdgeWAFModeBlock   = "block"
)

// EdgeWAFCRSRuleIDMin and EdgeWAFCRSRuleIDMax bound the OWASP Core Rule Set
// ID range. Exclusions outside it cannot name a CRS rule.
const (
	EdgeWAFCRSRuleIDMin = 900000
	EdgeWAFCRSRuleIDMax = 999999
)

// EdgeRuleWAFAction is the kind=waf payload (ADR-831 step 1). The rule's
// match host/path/methods select which requests are inspected with the
// OWASP Core Rule Set; the action tunes how a detection is scored.
type EdgeRuleWAFAction struct {
	// Mode is observe (default), warn, or block. Warn and block check
	// headers and URI in-path and require paranoia_level 1.
	Mode string `json:"mode,omitempty"`
	// ParanoiaLevel selects CRS paranoia level 1 or 2. Zero applies 1.
	ParanoiaLevel int `json:"paranoia_level,omitempty"`
	// AnomalyThreshold is the inbound anomaly score at which a request
	// counts as a detection. Zero applies the CRS default of 5.
	AnomalyThreshold int `json:"anomaly_threshold,omitempty"`
	// ExcludeRuleIDs lists CRS rule IDs whose matches do not count
	// toward the score, for known false positives on this route.
	ExcludeRuleIDs []int `json:"exclude_rule_ids,omitempty"`
	// InspectBodyBytes is how much of the request body is inspected.
	// Larger and streaming bodies are inspected up to this prefix only.
	// Zero applies EdgeWAFDefaultInspectBodyBytes.
	InspectBodyBytes int `json:"inspect_body_bytes,omitempty"`
}

// Validate applies the ADR-831 defaults and bounds, mutating the receiver so
// the stored row carries effective values.
func (a *EdgeRuleWAFAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("waf action is required")
	}
	if a.Mode == "" {
		a.Mode = EdgeWAFModeObserve
	}
	if a.Mode != EdgeWAFModeObserve && a.Mode != EdgeWAFModeWarn && a.Mode != EdgeWAFModeBlock {
		return ErrValidation(fmt.Sprintf(
			"waf action: mode must be one of observe, warn, block (got %q)", a.Mode))
	}
	if a.ParanoiaLevel == 0 {
		a.ParanoiaLevel = EdgeWAFDefaultParanoiaLevel
	}
	if a.ParanoiaLevel < 1 || a.ParanoiaLevel > MaxEdgeWAFParanoiaLevel {
		return ErrValidation(fmt.Sprintf(
			"waf action: paranoia_level must be in 1..%d (got %d)",
			MaxEdgeWAFParanoiaLevel, a.ParanoiaLevel))
	}
	if a.Mode != EdgeWAFModeObserve && a.ParanoiaLevel != 1 {
		return ErrValidation(fmt.Sprintf(
			"waf action: mode %q checks requests in-path at paranoia_level 1 only (got %d); use observe for level 2",
			a.Mode, a.ParanoiaLevel))
	}
	if a.AnomalyThreshold == 0 {
		a.AnomalyThreshold = EdgeWAFDefaultAnomalyThreshold
	}
	if a.AnomalyThreshold < 1 || a.AnomalyThreshold > MaxEdgeWAFAnomalyThreshold {
		return ErrValidation(fmt.Sprintf(
			"waf action: anomaly_threshold must be in 1..%d (got %d)",
			MaxEdgeWAFAnomalyThreshold, a.AnomalyThreshold))
	}
	if a.InspectBodyBytes == 0 {
		a.InspectBodyBytes = EdgeWAFDefaultInspectBodyBytes
	}
	if a.InspectBodyBytes < 1 || a.InspectBodyBytes > MaxEdgeWAFInspectBodyBytes {
		return ErrValidation(fmt.Sprintf(
			"waf action: inspect_body_bytes must be in 1..%d (got %d)",
			MaxEdgeWAFInspectBodyBytes, a.InspectBodyBytes))
	}
	return a.validateExclusions()
}

func (a *EdgeRuleWAFAction) validateExclusions() *Problem {
	if len(a.ExcludeRuleIDs) > MaxEdgeWAFExcludedRules {
		return ErrValidation(fmt.Sprintf(
			"waf action: exclude_rule_ids holds at most %d rule IDs (got %d)",
			MaxEdgeWAFExcludedRules, len(a.ExcludeRuleIDs)))
	}
	for _, id := range a.ExcludeRuleIDs {
		if id < EdgeWAFCRSRuleIDMin || id > EdgeWAFCRSRuleIDMax {
			return ErrValidation(fmt.Sprintf(
				"waf action: exclude_rule_ids entry %d is not a CRS rule ID (%d..%d)",
				id, EdgeWAFCRSRuleIDMin, EdgeWAFCRSRuleIDMax))
		}
	}
	slices.Sort(a.ExcludeRuleIDs)
	a.ExcludeRuleIDs = slices.Compact(a.ExcludeRuleIDs)
	return nil
}
