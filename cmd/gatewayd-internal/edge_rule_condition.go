package main

import (
	"log/slog"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// compileEdgeRuleCondition compiles a rule's ADR-962 match condition once,
// at host load. apid validates conditions on write, so a stored condition
// that no longer compiles (a direct-database edit, or a bound tightened
// since) makes the rule never match, and edgeRuleConditionError reports the
// same rule as a compile error so its host fails closed, as an unparseable
// match_path does.
//
// lists are the account lists the condition references (ADR-963), resolved
// at the same host load; an unresolved reference fails to compile.
func compileEdgeRuleCondition(ruleID, appID, mode string, expr *api.EdgeRuleMatchExpr, lists api.EdgeRuleLists) gateway.EdgeRuleCondition {
	cond := gateway.EdgeRuleCondition{RuleID: ruleID, AppID: appID, LogOnly: mode == state.EdgeRuleModeLog}
	program, err := api.CompileEdgeRuleMatchWithLists(expr, lists)
	if err != nil {
		slog.Warn("edge rule match condition does not compile; rule disabled", "rule", ruleID, "err", err)
		cond.Match = api.NeverMatchingEdgeRuleProgram()
		return cond
	}
	cond.Match = program
	return cond
}

// edgeRuleConditionError reports why a rule's match condition does not
// compile against the lists resolved for it, or nil when it has none or it
// compiles.
func edgeRuleConditionError(rule state.EdgeRule) error {
	if rule.Match == nil {
		return nil
	}
	_, err := api.CompileEdgeRuleMatchWithLists(rule.Match, rule.MatchLists)
	return err
}
