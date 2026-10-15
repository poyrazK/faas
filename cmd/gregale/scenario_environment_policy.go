package main

type testCampaignEnvironmentDecision struct {
	EnvironmentIndex int `json:"environment_index"`
	PolicyRule string `json:"policy_rule"`
	Action string `json:"action"`
	EffectivePolicy testCampaignPolicySettings `json:"effective_policy"`
}

func applyTestCampaignEnvironmentContexts(policy testCampaignPolicy, result *testCampaignPolicyResult) {
	result.EnvironmentDecisions = make([]testCampaignEnvironmentDecision, 0)
	for index := range result.Comparisons.Environments {
		row := &result.Comparisons.Environments[index]
		selected, ruleID := selectTestCampaignPolicy(policy, row.Context, row.Configuration, true)
		action := testCampaignEnvironmentAction(selected, row)
		settings := testCampaignSettings(selected)
		result.EnvironmentDecisions = append(result.EnvironmentDecisions, testCampaignEnvironmentDecision{
			EnvironmentIndex: index, PolicyRule: ruleID, Action: action, EffectivePolicy: settings,
		})
		if action == "ignore" || (action == "not_comparable" && selected.NotComparable != "fail") {
			continue
		}
		kind := "not_comparable"
		if action == "fail" {
			kind = "environment_mismatch"
			if row.Status == "unknown" {
				kind = "environment_unknown"
			}
		}
		result.Violations = append(result.Violations, testCampaignPolicyViolation{
			Rule: kind, PolicyRule: ruleID, Reason: row.Reason, EffectivePolicy: &settings, Environment: row,
			Context: row.Context, Configuration: row.Configuration,
			BeforeRuns: row.Baseline.ExecutedRuns, AfterRuns: row.Current.ExecutedRuns,
		})
	}
}

func testCampaignHasEnvironmentViolation(violations []testCampaignPolicyViolation, row *testEnvironmentComparison, ruleID string) bool {
	if row == nil {
		return false
	}
	for _, v := range violations {
		if v.Environment != nil && v.PolicyRule == ruleID && v.Configuration == row.Configuration &&
			v.Environment.Context == row.Context {
			return true
		}
	}
	return false
}
