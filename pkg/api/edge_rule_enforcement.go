package api

import "encoding/json"

// EdgeRuleEnforcementProblem reports why the gateway cannot enforce a stored
// rule, given its kind, whether it is enabled, and its action as
// EdgeRuleResponse.Action carries it; "" when it can. The gateway fails the
// hosts such a rule covers closed: every request to them gets HTTP 503
// naming the rule (ADR-091 amendments 2026-11-02 and 2026-11-03). A rule
// reaches that state when a release tightens the bounds a WAF or challenge
// value must meet after the rule was saved, or through a direct database
// write; apid refuses such values on every write.
func EdgeRuleEnforcementProblem(kind string, enabled bool, action json.RawMessage) string {
	if !enabled || len(action) == 0 {
		return ""
	}
	var stored struct {
		WAF       *EdgeRuleWAFAction       `json:"waf"`
		Challenge *EdgeRuleChallengeAction `json:"challenge"`
	}
	if json.Unmarshal(action, &stored) != nil {
		return ""
	}
	var prob *Problem
	switch {
	case kind == "waf" && stored.WAF != nil:
		prob = stored.WAF.Validate()
	case kind == "challenge" && stored.Challenge != nil:
		prob = stored.Challenge.Validate()
	}
	if prob == nil {
		return ""
	}
	return prob.Detail
}
