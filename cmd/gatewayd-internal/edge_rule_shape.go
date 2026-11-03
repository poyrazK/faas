package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// edgeRuleShapeError reports an enabled rule this gateway cannot enforce: a
// kind it has no compiler for, or a kind whose action payload is missing
// (ADR-091 amendment 2026-11-02, review round 8). Every compiler used to skip
// such a row without an error, so the rule silently stopped applying. Units
// release independently and the control plane upgrades before the edge, so a
// kind added in a later release reaches older gateways first; failing the
// host closed (counted, like any other broken rule) keeps a refusing rule from
// becoming a pass for the length of the rollout.
func edgeRuleShapeError(rule state.EdgeRule) error {
	if !rule.Enabled {
		return nil
	}
	present, known := edgeRulePayloadPresent(rule)
	switch {
	case !known:
		return fmt.Errorf("edge rule kind %q is not known to this gateway", rule.Kind)
	case !present:
		return fmt.Errorf("edge rule of kind %q has no %s action", rule.Kind, rule.Kind)
	}
	return nil
}

// edgeRulePayloadPresent reports whether rule carries its kind's action
// payload, and whether the kind is one this gateway compiles. Adding a kind
// to state.EdgeRuleKind without a case here fails
// TestEveryEdgeRuleKindHasAGatewayShape.
func edgeRulePayloadPresent(rule state.EdgeRule) (present, known bool) {
	a := rule.Action
	switch rule.Kind {
	case state.EdgeRuleKindRoute:
		return a.Route != nil, true
	case state.EdgeRuleKindRewrite:
		return a.Rewrite != nil, true
	case state.EdgeRuleKindRedirect:
		return a.Redirect != nil, true
	case state.EdgeRuleKindHeaders:
		return a.Headers != nil, true
	case state.EdgeRuleKindCORSA:
		return a.CORS != nil, true
	case state.EdgeRuleKindJWT:
		return a.JWT != nil, true
	case state.EdgeRuleKindIP:
		return a.IP != nil, true
	case state.EdgeRuleKindValidate:
		return a.Validate != nil, true
	case state.EdgeRuleKindLimit:
		return a.Limit != nil, true
	case state.EdgeRuleKindMaintenance:
		return a.Maintenance != nil, true
	case state.EdgeRuleKindGeo:
		return a.Geo != nil, true
	case state.EdgeRuleKindThrottle:
		return a.Throttle != nil, true
	case state.EdgeRuleKindBudget:
		return a.Budget != nil, true
	case state.EdgeRuleKindCache:
		return a.Cache != nil, true
	case state.EdgeRuleKindRespond:
		return a.Respond != nil, true
	case state.EdgeRuleKindRetry:
		return a.Retry != nil, true
	case state.EdgeRuleKindCircuitBreaker:
		return a.CircuitBreaker != nil, true
	case state.EdgeRuleKindAsync:
		return a.Async != nil, true
	case state.EdgeRuleKindWAF:
		return a.WAF != nil, true
	case state.EdgeRuleKindChallenge:
		return a.Challenge != nil, true
	}
	return false, false
}
