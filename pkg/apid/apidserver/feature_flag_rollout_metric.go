package apidserver

import (
	"context"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Metric-driven rollouts (ADR 20261119173702 §5): a stage with a metric
// advances only when its customers convert no worse than everyone else
// beyond the tolerance, and rolls back to 0 when they convert clearly
// worse.

// rolloutMetricStep decides a metric-gated rule's stage. It reports
// whether the stage may advance; a rollback publishes rollout 0 without
// the progression and reports false.
func (s *server) rolloutMetricStep(ctx context.Context, candidate state.FeatureFlagAutoRolloutCandidate, current state.FeatureFlagVersion, flagKey string, rule flags.Rule) (bool, error) {
	m := rule.Progression.Metric
	if m == nil {
		return true, nil
	}
	store, ok := s.store.(state.FeatureFlagRolloutMetricStore)
	if !ok {
		return false, nil
	}
	sc := candidate.Scope
	comparison, treatment, err := store.FeatureFlagRolloutMetricArms(ctx, state.RolloutMetricQuery{AccountID: sc.AccountID,
		ProjectID: sc.ProjectID, EnvironmentID: sc.EnvironmentID, EnvironmentSlug: candidate.EnvironmentSlug, FlagKey: flagKey,
		RuleID: rule.ID, Event: m.Event, Since: current.CreatedAt, MaxUnits: api.AnalyticsQueryMaxUnits})
	if err != nil {
		return false, err
	}
	decision, c := flags.RolloutMetricDecision(*m, comparison, treatment, api.FlagExperimentMixingLift, api.FlagExperimentAlpha)
	evidence := map[string]any{"project_id": sc.ProjectID, "environment_id": sc.EnvironmentID, "project": candidate.ProjectSlug,
		"environment": candidate.EnvironmentSlug, "flag": flagKey, "rule_id": rule.ID, "from_version": current.Version,
		"event": m.Event, "treatment": treatment, "comparison": comparison}
	if c != nil {
		evidence["difference"], evidence["low"], evidence["high"], evidence["p_value"] = c.Lift, c.Low, c.High, c.PValue
	}
	switch decision {
	case flags.RolloutAdvance:
		return true, nil
	case flags.RolloutRollback:
		return false, s.rollbackRolloutMetric(ctx, sc, current, flagKey, rule.ID, evidence)
	}
	if s.log != nil {
		s.log.InfoContext(ctx, "flags auto-advance: rollout held by metric", "flag", flagKey, "rule_id", rule.ID,
			"treatment", treatment.Exposed, "comparison", comparison.Exposed)
	}
	return false, nil
}

// rollbackRolloutMetric sets the rule's rollout to 0 and drops its
// progression, if the configuration is still the stage's version.
func (s *server) rollbackRolloutMetric(ctx context.Context, sc state.FeatureFlagScope, current state.FeatureFlagVersion, flagKey, ruleID string, evidence map[string]any) error {
	cfg := current.Config
	cfg.Flags = slices.Clone(cfg.Flags)
	fi := slices.IndexFunc(cfg.Flags, func(f flags.Flag) bool { return f.Key == flagKey })
	if fi < 0 {
		return nil
	}
	cfg.Flags[fi].Rules = slices.Clone(cfg.Flags[fi].Rules)
	ri := slices.IndexFunc(cfg.Flags[fi].Rules, func(r flags.Rule) bool { return r.ID == ruleID })
	if ri < 0 {
		return nil
	}
	zero := 0
	cfg.Flags[fi].Rules[ri].Rollout, cfg.Flags[fi].Rules[ri].Progression = &zero, nil
	store, ok := s.store.(state.FeatureFlagStore)
	if !ok {
		return nil
	}
	updated, err := store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: sc, ExpectedVersion: current.Version, Config: cfg,
		Actor: featureFlagAutoAdvanceActor})
	if errors.Is(err, state.ErrConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	evidence["version"] = updated.Version
	s.audit.EmitAs(ctx, featureFlagAutoAdvanceActor, "flags.rollout_auto_rolled_back", &sc.AccountID, evidence)
	if s.log != nil {
		s.log.WarnContext(ctx, "flags auto-advance: rollout rolled back by metric", "flag", flagKey, "rule_id", ruleID, "version", updated.Version)
	}
	return nil
}
