package edgerules

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SealEnvironmentPolicyHeaders seals the request header values of an
// environment edge policy's headers rules as an application rule's are
// (ADR-20261108110000, amendment 2026-11-09). The values belong to app: the
// gateway turns the policy into app's rules before it opens them. previous is
// the stored policy's rules; a redacted op takes the stored value of the same
// header, from the rule at the same position first. rules are changed in place.
func (s *Service) SealEnvironmentPolicyHeaders(ctx context.Context, app state.App, rules []state.ProjectEnvironmentEdgeRule, previous []state.ProjectEnvironmentEdgeRule) *api.Problem {
	return s.sealEnvironmentPolicyHeaders(ctx, app, rules, previous, nil)
}

func (s *Service) sealEnvironmentPolicyHeaders(ctx context.Context, app state.App, rules []state.ProjectEnvironmentEdgeRule, previous []state.ProjectEnvironmentEdgeRule, sealed map[string]string) *api.Problem {
	for i := range rules {
		if rules[i].Kind != state.EdgeRuleKindHeaders || rules[i].Action.Headers == nil {
			continue
		}
		prior := environmentPolicyPrevious(previous, i)
		if prob := s.sealHeaderSecretsWith(ctx, app, &rules[i].Action, prior, sealed); prob != nil {
			return prob
		}
	}
	return nil
}

// environmentPolicyPrevious gathers the stored request header ops a redacted
// op may name, those of the rule at position first.
func environmentPolicyPrevious(previous []state.ProjectEnvironmentEdgeRule, position int) *state.EdgeRule {
	var ops []state.EdgeRuleHeaderOp
	add := func(rule state.ProjectEnvironmentEdgeRule) {
		if rule.Kind == state.EdgeRuleKindHeaders && rule.Action.Headers != nil {
			ops = append(ops, rule.Action.Headers.RequestHeaders...)
		}
	}
	if position < len(previous) {
		add(previous[position])
	}
	for i, rule := range previous {
		if i != position {
			add(rule)
		}
	}
	if len(ops) == 0 {
		return nil
	}
	return &state.EdgeRule{Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{RequestHeaders: ops}}}
}

// RedactEnvironmentPolicyAction is RedactHeaderSecrets for a policy rule.
func RedactEnvironmentPolicyAction(rule state.ProjectEnvironmentEdgeRule) state.EdgeRuleAction {
	return RedactHeaderSecrets(rule.Action)
}

func (s *Service) backfillEnvironmentPolicies(ctx context.Context, sealed map[string]string, rep *HeaderSecretBackfillReport) error {
	literals, ok := s.Host.Store().(state.EnvironmentPolicyHeaderLiteralStore)
	if !ok || s.EnvironmentPolicyHost == nil {
		return nil
	}
	afterApp, afterEnvironment := "", ""
	for {
		page, err := literals.ListEnvironmentEdgePoliciesWithRequestHeaderLiterals(ctx, afterApp, afterEnvironment, headerSecretBackfillPage)
		if err != nil {
			return err
		}
		for _, policy := range page {
			afterApp, afterEnvironment = policy.AppID, policy.EnvironmentSlug
			legacy := false
			for _, rule := range policy.Rules {
				legacy = legacy || (rule.Kind == state.EdgeRuleKindHeaders && legacyHeaderValues(rule.Action))
			}
			if !legacy {
				continue
			}
			switch s.sealStoredEnvironmentPolicy(ctx, literals, policy, sealed) {
			case backfillSealed:
				rep.PoliciesSealed++
			case backfillChanged:
				rep.PoliciesChanged++
			case backfillFleetNotReady:
				rep.FleetNotReady = true
				return nil
			default:
				rep.PoliciesFailed++
			}
		}
		if len(page) < headerSecretBackfillPage {
			return nil
		}
	}
}

func (s *Service) sealStoredEnvironmentPolicy(ctx context.Context, literals state.EnvironmentPolicyHeaderLiteralStore, policy state.ProjectEnvironmentEdgePolicy, sealed map[string]string) backfillOutcome {
	log := s.Host.Logger().With("app", policy.AppID, "environment", policy.EnvironmentSlug)
	app, err := s.Host.Store().AppByID(ctx, policy.AppID)
	if err != nil {
		log.Warn("environment policy header backfill: read app failed", "err", err)
		return backfillFailed
	}
	rules := make([]state.ProjectEnvironmentEdgeRule, len(policy.Rules))
	for i, rule := range policy.Rules {
		rule.Action = RedactHeaderSecrets(rule.Action)
		rules[i] = rule
	}
	if prob := s.sealEnvironmentPolicyHeaders(ctx, app, rules, policy.Rules, sealed); prob != nil {
		log.Warn("environment policy header backfill: seal failed", "detail", prob.Detail)
		return backfillFailed
	}
	for _, rule := range rules {
		if rule.Kind == state.EdgeRuleKindHeaders && legacyHeaderValues(rule.Action) {
			return backfillFleetNotReady
		}
	}
	host, err := s.EnvironmentPolicyHost(ctx, policy)
	if err != nil {
		log.Warn("environment policy header backfill: environment host unavailable", "err", err)
		return backfillFailed
	}
	convergence, err := s.PrepareEdgeRuleMutation(ctx, policy.AppID, "", "environment_policy_updated", host)
	if err != nil {
		log.Warn("environment policy header backfill: convergence unavailable", "err", err)
		return backfillFailed
	}
	replaced, err := literals.ReplaceEnvironmentEdgePolicyRules(ctx, policy, rules)
	switch {
	case errors.Is(err, state.ErrEnvironmentGitManaged):
		convergence.Abort(ctx)
		return backfillChanged // a Git source took the policy over since the scan
	case err != nil:
		convergence.Abort(ctx)
		log.Warn("environment policy header backfill: update failed", "err", err)
		return backfillFailed
	case !replaced:
		convergence.Abort(ctx)
		return backfillChanged
	}
	s.Host.EmitAudit(ctx, "project.environment.policies.header_values_sealed", &policy.AccountID, map[string]any{
		"project_id": policy.ProjectID, "environment": policy.EnvironmentSlug, "app_id": policy.AppID,
	})
	if err := convergence.Apply(ctx, ""); err != nil {
		log.Warn("environment policy header backfill: sealed policy saved, fleet acknowledgement incomplete", "err", err)
	}
	return backfillSealed
}
