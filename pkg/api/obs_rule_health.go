package api

import "time"

// ObsRuleHealthResponse assesses the currently loaded alert rules. Totals cover
// the complete bounded source snapshot; Items is an unhealthy-first preview.
// It does not prove deployment of an expected inventory or a past failure cause.
type ObsRuleHealthResponse struct {
	GeneratedAt               time.Time            `json:"generated_at"`
	Scope                     string               `json:"scope"`
	State                     string               `json:"state"`
	Reason                    string               `json:"reason,omitempty"`
	MaxAgeSeconds             int                  `json:"max_age_seconds"`
	ClockSkewToleranceSeconds int                  `json:"clock_skew_tolerance_seconds"`
	Totals                    *ObsRuleHealthTotals `json:"totals"`
	Items                     []ObsRuleHealthItem  `json:"items"`
	TotalItems                int                  `json:"total_items"`
	Limit                     int                  `json:"limit"`
	Truncated                 bool                 `json:"truncated"`
	Limitations               []string             `json:"limitations"`
}

// Rule state categories partition Rules. InvalidGroups counts source groups
// with missing or ambiguous identity, including empty invalid groups.
type ObsRuleHealthTotals struct {
	Groups        int `json:"groups"`
	Rules         int `json:"rules"`
	Healthy       int `json:"healthy"`
	Failed        int `json:"failed"`
	Stale         int `json:"stale"`
	InvalidClock  int `json:"invalid_clock"`
	Unavailable   int `json:"unavailable"`
	Invalid       int `json:"invalid"`
	InvalidGroups int `json:"invalid_groups"`
}

// ObsRuleHealthItem carries safe rule/group identity and closed issue codes.
// Absolute paths, queries, labels, annotations and evaluation errors stay private.
// ReportedState is last reported state, not proof of current incident recovery.
// Counts are usable only for healthy items; nil counts never mean quiet.
type ObsRuleHealthItem struct {
	Kind                 string     `json:"kind"`
	ID                   string     `json:"id"`
	GroupID              string     `json:"group_id"`
	GroupName            string     `json:"group_name,omitempty"`
	FileName             string     `json:"file_name,omitempty"`
	RuleName             string     `json:"rule_name,omitempty"`
	State                string     `json:"state"`
	Issues               []string   `json:"issues"`
	ReportedState        string     `json:"reported_state"`
	LastEvaluatedAt      *time.Time `json:"last_evaluated_at,omitempty"`
	EvaluationAgeSeconds *int64     `json:"evaluation_age_seconds"`
	HasEvaluationError   bool       `json:"has_evaluation_error"`
	Pending              *int       `json:"pending"`
	Firing               *int       `json:"firing"`
	RunbookURL           string     `json:"runbook_url,omitempty"`
}
