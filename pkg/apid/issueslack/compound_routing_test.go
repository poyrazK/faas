// adr: 1662
package issueslack

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCompoundChannelRulesKeepThresholdNamespacesSeparate(t *testing.T) {
	rules := []state.IssueSlackRoutingRule{
		{Key: "frequency", Enabled: true, Event: "frequency_threshold_reached", FrequencyRuleKey: "payments", ChannelID: "C0123456789"},
		{Key: "compound", Enabled: true, Event: "compound_threshold_reached", FrequencyRuleKey: "payments", ChannelID: "C1111111111"},
	}
	rules, err := state.NormalizeIssueSlackRoutingRules(rules)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"frequency_threshold_reached", "compound_threshold_reached"} {
		key, channel := state.MatchIssueSlackRouting(rules, event, "production", "payments", "")
		if event == "compound_threshold_reached" && (key != "compound" || channel != "C1111111111") {
			t.Fatal(key, channel)
		}
		if event == "frequency_threshold_reached" && (key != "frequency" || channel != "C0123456789") {
			t.Fatal(key, channel)
		}
	}
	rules[1].Event = "created"
	if _, err := state.NormalizeIssueSlackRoutingRules(rules); err == nil {
		t.Fatal("threshold key accepted for lifecycle alert")
	}
}
