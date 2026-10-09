package main

import (
	"log/slog"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// compileEdgeRuleCondition compiles a rule's ADR-906 match condition once,
// at host load. apid validates conditions on write, so a stored condition
// that no longer compiles (a direct-database edit, or a bound tightened
// since) makes the rule never match — the same posture as an unparseable
// match_path, which drops the rule — and is logged for the operator.
//
// lists are the account lists the condition references (ADR-907), resolved
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
