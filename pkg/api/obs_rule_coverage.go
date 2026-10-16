package api

import "time"

// ObsRuleCoverage compares an explicitly selected expected set with one complete
// loaded alert-rule read. Covered definitions do not prove healthy evaluations.
type ObsRuleCoverage struct {
	GeneratedAt          time.Time              `json:"generated_at"`
	Scope                string                 `json:"scope"`
	FingerprintAlgorithm string                 `json:"fingerprint_algorithm"`
	SelectionSource      string                 `json:"selection_source"`
	State                string                 `json:"state"`
	Reason               string                 `json:"reason,omitempty"`
	ExpectedSet          *ObsRuleCoverageSet    `json:"expected_set"`
	AvailableSets        []ObsRuleCoverageSet   `json:"available_sets"`
	Totals               *ObsRuleCoverageTotals `json:"totals"`
	Items                []ObsRuleCoverageItem  `json:"items"`
	TotalItems           int                    `json:"total_items"`
	Limit                int                    `json:"limit"`
	Truncated            bool                   `json:"truncated"`
	Limitations          []string               `json:"limitations"`
}

type ObsRuleCoverageSet struct {
	ID            string   `json:"id"`
	Revision      string   `json:"revision"`
	Files         []string `json:"files"`
	ExpectedRules int      `json:"expected_rules"`
}

// Expected categories partition ExpectedRules. Observed counts cover the full
// source; additional rules are allowed by the selected minimum expectation set.
type ObsRuleCoverageTotals struct {
	ExpectedRules         int `json:"expected_rules"`
	ObservedRules         int `json:"observed_rules"`
	AdditionalRules       int `json:"additional_rules"`
	InvalidObservedRules  int `json:"invalid_observed_rules"`
	InvalidObservedGroups int `json:"invalid_observed_groups"`
	Matching              int `json:"matching"`
	Missing               int `json:"missing"`
	Changed               int `json:"changed"`
	Ambiguous             int `json:"ambiguous"`
	Unknown               int `json:"unknown"`
}

type ObsRuleCoverageItem struct {
	ID                  string   `json:"id"`
	FileName            string   `json:"file_name"`
	GroupName           string   `json:"group_name"`
	RuleName            string   `json:"rule_name"`
	State               string   `json:"state"`
	Reason              string   `json:"reason,omitempty"`
	ExpectedFingerprint string   `json:"expected_fingerprint"`
	ObservedFingerprint string   `json:"observed_fingerprint,omitempty"`
	ObservedCandidates  int      `json:"observed_candidates"`
	ChangedFields       []string `json:"changed_fields"`
}
