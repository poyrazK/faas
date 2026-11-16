package apidserver

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Guardrails, numeric metrics and recommendations (ADR 20261116190208).

// flagExperimentGuardrailActor publishes the rollback of a breached
// experiment.
const flagExperimentGuardrailActor = "system:flags-experiment-guardrail"

// errFlagExperimentRuleChanged reports that a rule no longer allocates by
// weight, so it cannot be switched to one variant.
var errFlagExperimentRuleChanged = errors.New("the experiment's rule is gone or no longer allocates by variant weight")

// defaultFlagExperimentGuardrails turns both guardrails on with their
// default tolerances and applies a create request's changes.
func defaultFlagExperimentGuardrails(req *api.FlagExperimentGuardrailsRequest) flags.GuardrailSettings {
	g := flags.GuardrailSettings{
		ServerErrorRate: flags.GuardrailSetting{MaxIncrease: api.FlagExperimentGuardrailErrorTolerance},
		P95Latency:      flags.GuardrailSetting{MaxIncrease: api.FlagExperimentGuardrailLatencyTolerance},
	}
	if req == nil {
		return g
	}
	apply := func(dst *flags.GuardrailSetting, src *api.FlagExperimentGuardrailRequest) {
		if src == nil {
			return
		}
		dst.Disabled = src.Disabled
		if src.MaxIncrease != nil {
			dst.MaxIncrease = *src.MaxIncrease
		}
	}
	apply(&g.ServerErrorRate, req.ServerErrorRate)
	apply(&g.P95Latency, req.P95Latency)
	return g
}

func validFlagExperimentGuardrails(req *api.FlagExperimentGuardrailsRequest) error {
	if req == nil {
		return nil
	}
	for name, g := range map[string]*api.FlagExperimentGuardrailRequest{flags.GuardrailServerErrorRate: req.ServerErrorRate, flags.GuardrailP95Latency: req.P95Latency} {
		if g != nil && g.MaxIncrease != nil && !(*g.MaxIncrease > 0 && *g.MaxIncrease <= 1) {
			return fmt.Errorf("guardrails.%s.max_increase is an absolute rate increase in (0, 1]", name)
		}
	}
	return nil
}

// flagExperimentGuardrails evaluates an experiment's enabled guardrails.
func flagExperimentGuardrails(x state.FlagExperiment, errs, slow []flags.RatioArm) []flags.GuardrailResult {
	out := []flags.GuardrailResult{}
	eval := func(name string, g flags.GuardrailSetting, arms []flags.RatioArm) {
		if g.Disabled {
			return
		}
		out = append(out, flags.EvaluateGuardrail(flags.GuardrailInput{Name: name, Tolerance: g.MaxIncrease, Control: x.Control,
			Arms: withWeightedArms(arms, x.Weights), Alpha: api.FlagExperimentGuardrailAlpha,
			MinUnits: api.FlagExperimentMinUnitsPerArm, MinRequests: api.FlagExperimentGuardrailMinRequests}))
	}
	eval(flags.GuardrailServerErrorRate, x.Guardrails.ServerErrorRate, errs)
	eval(flags.GuardrailP95Latency, x.Guardrails.P95Latency, slow)
	return out
}

// withWeightedArms adds an empty arm for every weighted variant nobody was
// exposed to, so each variant is reported.
func withWeightedArms(arms []flags.RatioArm, weights map[string]int) []flags.RatioArm {
	out := slices.Clone(arms)
	for v, w := range weights {
		if w > 0 && !slices.ContainsFunc(out, func(a flags.RatioArm) bool { return a.Variant == v }) {
			out = append(out, flags.RatioArm{Variant: v})
		}
	}
	return out
}

// serveFlagExperimentVariant publishes the experiment's flag configuration
// with its rule serving variant to every customer it matches. It returns
// the configuration version that serves it; a rule that already serves the
// variant is left as is. A concurrent configuration change is retried.
func (s *server) serveFlagExperimentVariant(ctx context.Context, x state.FlagExperiment, variant, actor string) (int64, error) {
	store, ok := s.store.(state.FeatureFlagStore)
	if !ok {
		return 0, errors.New("feature flags unavailable")
	}
	scope := state.FeatureFlagScope{AccountID: x.AccountID, ProjectID: x.ProjectID, EnvironmentID: x.EnvironmentID}
	for attempt := 0; attempt < 3; attempt++ {
		current, err := store.GetFeatureFlags(ctx, scope, 0)
		if err != nil {
			return 0, fmt.Errorf("read flags: %w", err)
		}
		if ruleServes(current.Config, x.FlagKey, x.RuleID, variant) {
			return current.Version, nil
		}
		next, ok := flags.ServeVariant(current.Config, x.FlagKey, x.RuleID, variant)
		if !ok {
			return 0, errFlagExperimentRuleChanged
		}
		updated, err := store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: current.Version, Config: next, Actor: actor})
		if errors.Is(err, state.ErrConflict) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("publish flags: %w", err)
		}
		return updated.Version, nil
	}
	return 0, fmt.Errorf("publish flags: %w", state.ErrConflict)
}

func ruleServes(cfg flags.Config, flagKey, ruleID, variant string) bool {
	for _, f := range cfg.Flags {
		if f.Key != flagKey {
			continue
		}
		for _, r := range f.Rules {
			if r.ID == ruleID {
				v, ok := r.Value.(string)
				return ok && v == variant
			}
		}
	}
	return false
}

// flagExperimentCommands are the CLI commands that carry out a
// recommendation.
func flagExperimentCommands(rec flags.Recommendation, x state.FlagExperiment, project, environment string) []string {
	where := fmt.Sprintf("--project %s --environment %s", project, environment)
	running := x.Status == flags.ExperimentRunning
	switch rec.Action {
	case flags.RecommendShipVariant:
		return []string{fmt.Sprintf("gregale flag experiment promote %s %s %s", x.Key, rec.Variant, where)}
	case flags.RecommendKeepControl, flags.RecommendFixAllocation:
		if running {
			return []string{fmt.Sprintf("gregale flag experiment stop %s %s", x.Key, where)}
		}
	case flags.RecommendDecide:
		return []string{fmt.Sprintf("gregale flag experiment promote %s %s %s", x.Key, x.Control, where)}
	}
	return nil
}

// flagExperimentLatencies reports each weighted variant's p95 latency.
func flagExperimentLatencies(x state.FlagExperiment, hist map[string]map[int]int64) []api.FlagExperimentLatency {
	variants := make([]string, 0, len(x.Weights))
	for v := range x.Weights {
		variants = append(variants, v)
	}
	slices.Sort(variants)
	out := make([]api.FlagExperimentLatency, 0, len(variants))
	for _, v := range variants {
		var n int64
		for _, c := range hist[v] {
			n += c
		}
		out = append(out, api.FlagExperimentLatency{Variant: v, Requests: n, P95MS: flags.HistogramQuantile(hist[v], 0.95)})
	}
	return out
}

func guardrailsResponse(results []flags.GuardrailResult) []api.FlagExperimentGuardrail {
	out := make([]api.FlagExperimentGuardrail, 0, len(results))
	for _, g := range results {
		ag := api.FlagExperimentGuardrail{Name: g.Name, Tolerance: g.Tolerance, Status: g.Status, Variants: []api.FlagExperimentGuardrailVariant{}}
		for _, v := range g.Variants {
			av := api.FlagExperimentGuardrailVariant{Variant: v.Variant, ControlRate: v.ControlRate, VariantRate: v.VariantRate,
				ControlUnits: v.ControlUnits, VariantUnits: v.VariantUnits, Status: v.Status}
			if c := v.Comparison; c != nil {
				av.Difference, av.Low, av.High = &c.Lift, &c.Low, &c.High
			}
			ag.Variants = append(ag.Variants, av)
		}
		out = append(out, ag)
	}
	return out
}

func guardrailSettingsResponse(g flags.GuardrailSettings) api.FlagExperimentGuardrailSettings {
	one := func(s flags.GuardrailSetting) api.FlagExperimentGuardrailSetting {
		return api.FlagExperimentGuardrailSetting{Enabled: !s.Disabled, MaxIncrease: s.MaxIncrease}
	}
	return api.FlagExperimentGuardrailSettings{ServerErrorRate: one(g.ServerErrorRate), P95Latency: one(g.P95Latency)}
}

// breachRecord describes a guardrail breach for the experiment row and the
// audit log.
func breachRecord(g flags.GuardrailResult, v flags.GuardrailVariant, version int64, rolledBack bool) map[string]any {
	out := map[string]any{"guardrail": g.Name, "variant": v.Variant, "tolerance": g.Tolerance,
		"control_rate": v.ControlRate, "variant_rate": v.VariantRate, "rolled_back": rolledBack}
	if c := v.Comparison; c != nil {
		out["difference"], out["low"], out["high"] = c.Lift, c.Low, c.High
	}
	if rolledBack {
		out["config_version"] = version
	}
	return out
}
