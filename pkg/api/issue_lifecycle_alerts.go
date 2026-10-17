package api

import (
	"fmt"
	"slices"
	"time"
)

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

func NormalizeIssueLifecycleAlertPolicy(environments []string, cooldown int64) ([]string, error) {
	if environments == nil || len(environments) > IssueLifecycleAlertMaxEnvironments {
		return nil, fmt.Errorf("environments must be an array of at most %d names; use [] for all environments", IssueLifecycleAlertMaxEnvironments)
	}
	if cooldown < 0 || cooldown > IssueLifecycleAlertMaxCooldownSeconds {
		return nil, fmt.Errorf("cooldown_seconds must be between 0 and %d", IssueLifecycleAlertMaxCooldownSeconds)
	}
	out := append([]string{}, environments...)
	for _, environment := range out {
		if !issueViewEnvironmentPattern.MatchString(environment) {
			return nil, fmt.Errorf("invalid alert environment")
		}
	}
	slices.Sort(out)
	if len(slices.Compact(out)) != len(environments) {
		return nil, fmt.Errorf("alert environments must be unique")
	}
	return out, nil
}
