package api

import (
	"time"
)

// IssueCompoundAlertRule requires both thresholds in the same retained-event window.
type IssueCompoundAlertRule struct {
	Key              string `json:"key"`
	Enabled          bool   `json:"enabled"`
	Environment      string `json:"environment"`
	MinimumEvents    int64  `json:"minimum_events"`
	MinimumCustomers int64  `json:"minimum_customers"`
	WindowSeconds    int64  `json:"window_seconds"`
	CooldownSeconds  int64  `json:"cooldown_seconds"`
}

type IssueCompoundAlertPolicy struct {
	Revision int64                    `json:"revision"`
	Rules    []IssueCompoundAlertRule `json:"rules"`
}

type UpdateIssueCompoundAlertPolicyRequest struct {
	ExpectedRevision *int64                   `json:"expected_revision"`
	Rules            []IssueCompoundAlertRule `json:"rules"`
}

type PreviewIssueCompoundAlertRequest struct {
	IssueID string                 `json:"issue_id"`
	Rule    IssueCompoundAlertRule `json:"rule"`
}

type IssueCompoundAlertPreview struct {
	IssueID             string     `json:"issue_id"`
	RuleKey             string     `json:"rule_key"`
	Environment         string     `json:"environment"`
	PolicyRevision      int64      `json:"policy_revision"`
	WindowStart         time.Time  `json:"window_start"`
	WindowEnd           time.Time  `json:"window_end"`
	ObservedEvents      int64      `json:"observed_events"`
	IdentifiedCustomers int64      `json:"identified_customers"`
	Enabled             bool       `json:"enabled"`
	EnvironmentMatches  bool       `json:"environment_matches"`
	EventsMatched       bool       `json:"events_matched"`
	CustomersMatched    bool       `json:"customers_matched"`
	Suppressed          bool       `json:"suppressed"`
	CooldownReady       bool       `json:"cooldown_ready"`
	Allowed             bool       `json:"allowed"`
	Reason              string     `json:"reason"`
	NextEligibleAt      *time.Time `json:"next_eligible_at,omitempty"`
}
