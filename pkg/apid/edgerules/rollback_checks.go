package edgerules

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// restoreProblem runs the checks a new rule passes on every rule a rollback
// would restore (ADR-091 amendment 2026-10-28). The store applies the plan's
// rule counts; the rest used to be skipped, so a rollback could bring back a
// kind the plan or the account's release no longer allows, a rule whose list
// was deleted since (which then never matched), a route to an app that has
// turned internal, or an async callback to a webhook that is gone. Action
// bounds that depend on the plan are clamped again by the gateway when it
// compiles the rule.
//
// current is the app's rule set now. As on update, a feature a restored rule
// already enforces under the same ID is not refused, so withdrawing a flag
// does not block rolling back over that rule.
func (s *Service) restoreProblem(ctx context.Context, acct state.Account, app state.App, restored, current []state.EdgeRule) *api.Problem {
	enforced := make(map[string][]edgeFeature, len(current))
	for _, rule := range current {
		if shape := storedRuleShape(rule); shape.active {
			enforced[rule.ID] = shape.features()
		}
	}
	for _, rule := range restored {
		if rule.AttackMode() {
			continue // the restore keeps the current attack-mode rule
		}
		if prob := s.restoredRuleProblem(ctx, acct, app, rule, enforced[rule.ID]); prob != nil {
			out := *prob
			out.Detail = fmt.Sprintf("the version's %s rule %s cannot be restored: %s", rule.Kind, rule.ID, prob.Detail)
			return &out
		}
	}
	return nil
}

func (s *Service) restoredRuleProblem(ctx context.Context, acct state.Account, app state.App, rule state.EdgeRule, enforced []edgeFeature) *api.Problem {
	kind := string(rule.Kind)
	switch {
	case rule.Kind == state.EdgeRuleKindRespond && app.PreviewOfSlug == "":
		return api.ErrValidation("respond edge rules are only allowed on preview applications")
	case rule.Kind == state.EdgeRuleKindAsync && !app.AcceptsRequestInvocations():
		return api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode)
	}
	if _, prob := api.NormalizeEdgeRuleMatchMethods(rule.MatchMethods); prob != nil {
		return prob
	}
	if prob := s.kindProblem(acct, kind); prob != nil {
		return prob
	}
	if shape := storedRuleShape(rule); shape.active {
		if prob := s.releaseProblem(acct, shape.features(), enforced); prob != nil {
			return prob
		}
	}
	if prob := s.validateEdgeRuleMatchLists(ctx, acct.ID, rule.Match); prob != nil {
		return prob
	}
	// The stored route and async actions carry the wire field names, so they
	// marshal back into the shapes the create-time checks read.
	switch {
	case rule.Kind == state.EdgeRuleKindRoute && rule.Action.Route != nil:
		raw, err := json.Marshal(rule.Action.Route)
		if err != nil {
			return api.ErrCapacity("could not check the route target")
		}
		return s.RouteTargetProblem(ctx, acct.ID, kind, raw)
	case rule.Kind == state.EdgeRuleKindAsync && rule.Action.Async != nil:
		raw, err := json.Marshal(rule.Action.Async)
		if err != nil {
			return api.ErrCapacity("could not check the async destinations")
		}
		return s.ValidateEdgeRuleAsyncDestinations(ctx, app.ID, acct.ID, kind, raw)
	}
	return nil
}
