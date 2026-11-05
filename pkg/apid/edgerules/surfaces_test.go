package edgerules

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261023113721 — a rule response names the gated features the rule
// uses that the account's flags do not release, whether or not the rule is
// enabled, and nothing once they are released.
func TestRuleResponseNamesUnreleasedFeatures(t *testing.T) {
	acct := state.Account{ID: "acct"}
	challenge := state.EdgeRule{Kind: state.EdgeRuleKindChallenge, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindChallenge}}
	wafBlock := state.EdgeRule{Kind: state.EdgeRuleKindWAF, Enabled: true,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindWAF, WAF: &state.EdgeRuleWAFAction{Mode: api.EdgeWAFModeBlock}, Penalty: &state.EdgeRulePenalty{Seconds: 600}}}
	wafObserve := state.EdgeRule{Kind: state.EdgeRuleKindWAF, Enabled: true, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindWAF, WAF: &state.EdgeRuleWAFAction{}}}
	none := &Service{Host: releaseHost{}}
	all := &Service{Host: releaseHost{released: map[string]bool{
		platformflags.EdgeChallenge: true, platformflags.EdgeWAFBlocking: true, platformflags.EdgePenaltyBox: true}}}
	for name, tc := range map[string]struct {
		svc  *Service
		row  state.EdgeRule
		want []string
	}{
		"disabled challenge, unreleased": {none, challenge, []string{api.EdgeFeatureChallenge}},
		"block with penalty, unreleased": {none, wafBlock, []string{api.EdgeFeatureWAFBlocking, api.EdgeFeaturePenaltyBox}},
		"observe WAF":                    {none, wafObserve, nil},
		"everything released":            {all, wafBlock, nil},
	} {
		if got := tc.svc.RuleResponse(acct, tc.row).UnreleasedFeatures; !slices.Equal(got, tc.want) {
			t.Errorf("%s: unreleased = %v, want %v", name, got, tc.want)
		}
	}
	if !all.FeatureReleased(acct, api.EdgeFeatureChallenge) || none.FeatureReleased(acct, api.EdgeFeatureChallenge) || all.FeatureReleased(acct, "unknown") {
		t.Error("FeatureReleased does not follow the flags")
	}
}
