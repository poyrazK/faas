package api

import "time"

// IssueLifecycleAlertPolicy filters native new-issue and regression Slack alerts.
// Empty environments and zero cooldown preserve the existing delivery behavior.
type IssueLifecycleAlertPolicy struct {
	Revision        int64    `json:"revision"`
	Environments    []string `json:"environments"`
	CooldownSeconds int64    `json:"cooldown_seconds"`
}

type UpdateIssueLifecycleAlertPolicyRequest struct {
	ExpectedRevision *int64   `json:"expected_revision"`
	Environments     []string `json:"environments"`
	CooldownSeconds  int64    `json:"cooldown_seconds"`
}

type PreviewIssueLifecycleAlertRequest struct {
	IssueID         string   `json:"issue_id"`
	Event           string   `json:"event"`
	Environments    []string `json:"environments"`
	CooldownSeconds int64    `json:"cooldown_seconds"`
}

type IssueLifecycleAlertPreview struct {
	IssueID        string     `json:"issue_id"`
	Event          string     `json:"event"`
	Environment    string     `json:"environment"`
	PolicyRevision int64      `json:"policy_revision"`
	Allowed        bool       `json:"allowed"`
	Reason         string     `json:"reason"`
	NextEligibleAt *time.Time `json:"next_eligible_at,omitempty"`
}
