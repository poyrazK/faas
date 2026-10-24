package edgerules

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// edgeFeature is an edge rule feature a platform flag releases
// (ADR-20261023113721); name is how a refusal names it.
type edgeFeature struct{ flag, name string }

var (
	featureChallenge   = edgeFeature{platformflags.EdgeChallenge, "Challenge rules"}
	featureWAFBlocking = edgeFeature{platformflags.EdgeWAFBlocking, "WAF warn and block modes"}
	featurePenaltyBox  = edgeFeature{platformflags.EdgePenaltyBox, "Edge rule penalties"}
)

// ruleShape is what the release gate reads from a rule.
type ruleShape struct {
	kind    string
	wafMode string // effective; observe when unset
	penalty bool
	// active is enabled and not in log mode (ADR-960).
	active bool
}

// features lists the flag-gated features the rule uses.
func (r ruleShape) features() []edgeFeature {
	var out []edgeFeature
	if r.kind == string(state.EdgeRuleKindChallenge) {
		out = append(out, featureChallenge)
	}
	if r.kind == string(state.EdgeRuleKindWAF) && (r.wafMode == api.EdgeWAFModeWarn || r.wafMode == api.EdgeWAFModeBlock) {
		out = append(out, featureWAFBlocking)
	}
	if r.penalty {
		out = append(out, featurePenaltyBox)
	}
	return out
}

// storedRuleShape is a stored rule's shape.
func storedRuleShape(row state.EdgeRule) ruleShape {
	shape := ruleShape{
		kind: string(row.Kind), penalty: row.Action.Penalty != nil,
		active: row.Enabled && row.Mode != api.EdgeRuleModeLog,
	}
	if row.Kind == state.EdgeRuleKindWAF {
		shape.wafMode = api.EdgeWAFModeObserve
		if row.Action.WAF != nil && row.Action.WAF.Mode != "" {
			shape.wafMode = row.Action.WAF.Mode
		}
	}
	return shape
}

// updatedRuleShape is row's shape once req applies.
func updatedRuleShape(row state.EdgeRule, req api.UpdateEdgeRuleRequest) ruleShape {
	shape := storedRuleShape(row)
	if req.Action != nil {
		shape.wafMode = wafModeFromAction(shape.kind, *req.Action)
	}
	switch {
	case req.ClearPenalty:
		shape.penalty = false
	case req.Penalty != nil:
		shape.penalty = true
	}
	enabled, mode := row.Enabled, row.Mode
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.Mode != nil && *req.Mode != "" {
		mode = *req.Mode
	}
	shape.active = enabled && mode != api.EdgeRuleModeLog
	return shape
}

// createReleaseProblem refuses a new rule that uses an edge feature the
// platform flags have not released to the account, whether or not the rule
// starts enabled.
func (s *Service) createReleaseProblem(acct state.Account, req api.CreateEdgeRuleRequest) *api.Problem {
	shape := ruleShape{kind: req.Kind, wafMode: wafModeFromAction(req.Kind, req.Action), penalty: req.Penalty != nil}
	return s.releaseProblem(acct, shape.features(), nil)
}

// updateReleaseProblem refuses an update that switches on an unreleased
// feature the rule does not already enforce. An edit that leaves an
// enforced feature as it was, disabling a rule, putting it in log mode and
// removing a feature are never refused, so withdrawing a flag strands no
// rule.
func (s *Service) updateReleaseProblem(acct state.Account, row state.EdgeRule, req api.UpdateEdgeRuleRequest) *api.Problem {
	after := updatedRuleShape(row, req)
	if !after.active {
		return nil
	}
	var enforced []edgeFeature
	if before := storedRuleShape(row); before.active {
		enforced = before.features()
	}
	return s.releaseProblem(acct, after.features(), enforced)
}

func (s *Service) releaseProblem(acct state.Account, used, enforced []edgeFeature) *api.Problem {
	for _, f := range used {
		if !slices.Contains(enforced, f) && !s.Host.PlatformFlagEnabled(f.flag, acct) {
			return api.ErrEdgeFeatureUnavailable(f.name)
		}
	}
	return nil
}
