package api

import (
	"fmt"
	"regexp"
	"sort"
)

// IssueFrequencyAlertRule counts retained accepted occurrences per canonical issue.
type IssueFrequencyAlertRule struct {
	Key             string `json:"key"`
	Enabled         bool   `json:"enabled"`
	Environment     string `json:"environment"`
	MinimumEvents   int64  `json:"minimum_events"`
	WindowSeconds   int64  `json:"window_seconds"`
	CooldownSeconds int64  `json:"cooldown_seconds"`
}

type IssueFrequencyAlertPolicy struct {
	Revision int64                     `json:"revision"`
	Rules    []IssueFrequencyAlertRule `json:"rules"`
}

type UpdateIssueFrequencyAlertPolicyRequest struct {
	ExpectedRevision *int64                    `json:"expected_revision"`
	Rules            []IssueFrequencyAlertRule `json:"rules"`
}

var issueFrequencyRuleKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

func NormalizeIssueFrequencyAlertRules(in []IssueFrequencyAlertRule) ([]IssueFrequencyAlertRule, error) {
	if len(in) > IssueFrequencyAlertMaxRules {
		return nil, fmt.Errorf("at most %d frequency alert rules are allowed", IssueFrequencyAlertMaxRules)
	}
	out := append([]IssueFrequencyAlertRule{}, in...)
	seen := map[string]bool{}
	for _, r := range out {
		if !issueFrequencyRuleKey.MatchString(r.Key) || seen[r.Key] {
			return nil, fmt.Errorf("rule keys must be unique and contain 1–48 lowercase letters, digits or hyphens")
		}
		seen[r.Key] = true
		if r.Environment != "" && !issueViewEnvironmentPattern.MatchString(r.Environment) {
			return nil, fmt.Errorf("invalid rule environment")
		}
		if r.MinimumEvents < 1 || r.MinimumEvents > IssueFrequencyAlertMaxEvents {
			return nil, fmt.Errorf("minimum_events must be between 1 and %d", IssueFrequencyAlertMaxEvents)
		}
		if r.WindowSeconds < IssueFrequencyAlertMinWindowSeconds || r.WindowSeconds > IssueFrequencyAlertMaxWindowSeconds {
			return nil, fmt.Errorf("window_seconds must be between %d and %d", IssueFrequencyAlertMinWindowSeconds, IssueFrequencyAlertMaxWindowSeconds)
		}
		if r.CooldownSeconds < IssueFrequencyAlertMinCooldownSeconds || r.CooldownSeconds > IssueFrequencyAlertMaxCooldownSeconds {
			return nil, fmt.Errorf("cooldown_seconds must be between %d and %d", IssueFrequencyAlertMinCooldownSeconds, IssueFrequencyAlertMaxCooldownSeconds)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
