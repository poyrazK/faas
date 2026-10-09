package main

import (
	"log/slog"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// compileEdgeRuleCondition compiles a rule's ADR-832 match condition once,
// at host load. apid validates conditions on write, so a stored condition
// that no longer compiles (a direct-database edit, or a bound tightened
// since) makes the rule never match — the same posture as an unparseable
// match_path, which drops the rule — and is logged for the operator.
func compileEdgeRuleCondition(ruleID string, expr *api.EdgeRuleMatchExpr) gateway.EdgeRuleCondition {
	program, err := api.CompileEdgeRuleMatch(expr)
	if err != nil {
		slog.Warn("edge rule match condition does not compile; rule disabled", "rule", ruleID, "err", err)
		return gateway.EdgeRuleCondition{Match: api.NeverMatchingEdgeRuleProgram()}
	}
	return gateway.EdgeRuleCondition{Match: program}
}
