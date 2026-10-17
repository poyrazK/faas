package state

import (
	"context"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type IssueSlackRoutingRule struct {
	Key              string `json:"key"`
	Enabled          bool   `json:"enabled"`
	Event            string `json:"event"`
	Environment      string `json:"environment"`
	FrequencyRuleKey string `json:"frequency_rule_key"`
	TeamID           string `json:"team_id"`
	ChannelID        string `json:"channel_id"`
}
type IssueSlackRoutingPolicy struct {
	Revision, InstallationRevision int64
	Active                         bool
	Rules                          []IssueSlackRoutingRule
}
type PutIssueSlackRoutingPolicy struct {
	Revision, InstallationRevision int64
	Rules                          []IssueSlackRoutingRule
}
type IssueSlackRoutingSample struct{ Event, Environment, FrequencyRuleKey, AssignedTeamID string }
type IssueSlackRoutingPreview struct {
	Outcome, TeamID, TeamName, DefaultChannelID, ChannelID, RuleKey string
	Revision, InstallationRevision                                  int64
}
type IssueSlackRoutingStore interface {
	GetIssueSlackRoutingPolicy(context.Context, string, string) (IssueSlackRoutingPolicy, error)
	SetIssueSlackRoutingPolicy(context.Context, string, string, PutIssueSlackRoutingPolicy) (IssueSlackRoutingPolicy, error)
	PreviewIssueSlackRouting(context.Context, string, string, PutIssueSlackRoutingPolicy, IssueSlackRoutingSample) (IssueSlackRoutingPreview, error)
}

var slackRoutingValue = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func ValidSlackRoutingKey(key string) bool {
	return len(key) <= api.IssueSlackRoutingKeyMaxBytes && slackRoutingValue.MatchString(key)
}
func ValidSlackRoutingEnvironment(env string) bool {
	return env == "" || len(env) <= api.IssueSlackRoutingEnvironmentMaxBytes && slackRoutingValue.MatchString(env)
}
func NormalizeIssueSlackRoutingRules(in []IssueSlackRoutingRule) ([]IssueSlackRoutingRule, error) {
	if len(in) > api.IssueSlackRoutingMaxRules {
		return nil, fmt.Errorf("at most %d Slack channel rules are allowed", api.IssueSlackRoutingMaxRules)
	}
	out := append([]IssueSlackRoutingRule{}, in...)
	seen := map[string]bool{}
	for i, r := range out {
		if !ValidSlackRoutingKey(r.Key) || seen[r.Key] {
			return nil, fmt.Errorf("use unique rule keys with 1–%d lowercase letters, digits or hyphens", api.IssueSlackRoutingKeyMaxBytes)
		}
		seen[r.Key] = true
		if !ApplicationSlackEvent(r.Event) || !ValidSlackRoutingEnvironment(r.Environment) || !ValidIssueSlackChannel(r.ChannelID) {
			return nil, fmt.Errorf("choose a native alert type, valid environment and Slack channel ID")
		}
		if r.FrequencyRuleKey != "" && (!ValidSlackRoutingKey(r.FrequencyRuleKey) || r.Event != "frequency_threshold_reached") {
			return nil, fmt.Errorf("a frequency-rule key only applies to frequency-threshold alerts")
		}
		if r.TeamID != "" {
			id, err := uuid.Parse(r.TeamID)
			if err != nil || id == uuid.Nil {
				return nil, fmt.Errorf("choose a valid team")
			}
			out[i].TeamID = id.String()
		}
	}
	return out, nil
}

// Rules preserve owner-defined priority. A channel override never chooses a team.
func MatchIssueSlackRouting(rules []IssueSlackRoutingRule, event, environment, frequencyKey, team string) (string, string) {
	for _, r := range rules {
		if r.Enabled && r.Event == event && (r.Environment == "" || r.Environment == environment) && (r.FrequencyRuleKey == "" || r.FrequencyRuleKey == frequencyKey) && (r.TeamID == "" || r.TeamID == team) {
			return r.Key, r.ChannelID
		}
	}
	return "", ""
}
