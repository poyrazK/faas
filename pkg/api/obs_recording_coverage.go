package api

import "time"

// ObsRecordingCoverage compares recording definitions. Expected syntactic alert
// references are separate from live signal health and incident causation.
type ObsRecordingCoverage struct {
	GeneratedAt          time.Time                      `json:"generated_at"`
	Scope                string                         `json:"scope"`
	FingerprintAlgorithm string                         `json:"fingerprint_algorithm"`
	SelectionSource      string                         `json:"selection_source"`
	State                string                         `json:"state"`
	Reason               string                         `json:"reason,omitempty"`
	ExpectedSet          *ObsRuleCoverageSet            `json:"expected_set"`
	AvailableSets        []ObsRuleCoverageSet           `json:"available_sets"`
	Totals               *ObsRuleCoverageTotals         `json:"totals"`
	Items                []ObsRecordingCoverageItem     `json:"items"`
	TotalItems           int                            `json:"total_items"`
	Limit                int                            `json:"limit"`
	Truncated            bool                           `json:"truncated"`
	Dependencies         *ObsRecordingDependencyMapping `json:"dependencies"`
	Limitations          []string                       `json:"limitations"`
}

type ObsRecordingCoverageItem struct {
	Comparison      ObsRuleCoverageItem         `json:"comparison"`
	DependentAlerts ObsRecordingAlertReferences `json:"dependent_alerts"`
}

type ObsRecordingAlertReference struct {
	ID        string `json:"id"`
	FileName  string `json:"file_name"`
	GroupName string `json:"group_name"`
	RuleName  string `json:"rule_name"`
}

type ObsRecordingAlertReferences struct {
	Items      []ObsRecordingAlertReference `json:"items"`
	TotalItems int                          `json:"total_items"`
	Limit      int                          `json:"limit"`
	Truncated  bool                         `json:"truncated"`
}

type ObsRecordingDependencyMapping struct {
	Scope            string                      `json:"scope"`
	State            string                      `json:"state"`
	AlertSetRevision string                      `json:"alert_set_revision"`
	TotalAlerts      int                         `json:"total_alerts"`
	UncertainAlerts  ObsRecordingAlertReferences `json:"uncertain_alerts"`
}
