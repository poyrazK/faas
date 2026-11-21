package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// An experiment's unit, layer, allocation and ratio or percentile settings
// (ADR 20261119173702).

// flagExperimentDesign is the validated design of a create request.
type flagExperimentDesign struct {
	unit, layer, allocation, denominator string
	traffic, percentile                  int
}

func invalidExperiment(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid experiment", detail)
}

// flagExperimentDesignFrom validates the request's unit, layer, allocation
// and metric settings against its metric kind.
func (s *server) flagExperimentDesignFrom(req api.FlagExperimentCreateRequest, kind string, acct state.Account) (flagExperimentDesign, *api.Problem) {
	d := flagExperimentDesign{unit: orCustomer(req.Unit), layer: req.Layer, traffic: req.Traffic, allocation: req.Allocation,
		denominator: req.DenominatorEvent, percentile: req.Percentile}
	if d.allocation == "" {
		d.allocation = flags.ExperimentAllocationFixed
	}
	if p := validExperimentShape(d, kind); p != nil {
		return d, p
	}
	if d.unit == flags.ExperimentUnitSubject {
		if !s.productAnalyticsOn(acct.ID, string(acct.Plan)) {
			return d, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Product analytics unavailable",
				"subject experiments read product analytics events, which this account's plan does not keep")
		}
		if req.Guardrails != nil {
			return d, invalidExperiment("subject experiments have no guardrails: request telemetry is per customer, not per subject")
		}
	}
	return d, nil
}

func validExperimentShape(d flagExperimentDesign, kind string) *api.Problem {
	switch {
	case d.unit != flags.ExperimentUnitCustomer && d.unit != flags.ExperimentUnitSubject:
		return invalidExperiment("unit is customer or subject")
	case d.unit == flags.ExperimentUnitSubject && !flags.BinaryMetric(kind):
		return invalidExperiment("subject experiments take a conversion, funnel or retention metric")
	case d.allocation != flags.ExperimentAllocationFixed && d.allocation != flags.ExperimentAllocationBandit:
		return invalidExperiment("allocation is fixed or bandit")
	case d.allocation == flags.ExperimentAllocationBandit && !flags.BinaryMetric(kind):
		return invalidExperiment("a bandit takes a conversion, funnel or retention metric")
	case d.layer == "" && d.traffic != 0:
		return invalidExperiment("traffic is a layer's share; it needs layer")
	case d.layer != "" && !flags.ValidKey(d.layer):
		return invalidExperiment("layer is a layer key")
	case d.layer != "" && (d.traffic < 1 || d.traffic > 10000):
		return invalidExperiment("traffic is the layer share in basis points, 1 to 10000")
	case kind == flags.ExperimentMetricRatio && !flags.ValidEventName(d.denominator):
		return invalidExperiment("a ratio metric needs denominator_event, an event name")
	case kind != flags.ExperimentMetricRatio && d.denominator != "":
		return invalidExperiment("denominator_event is only for ratio metrics")
	case kind == flags.ExperimentMetricPercentile && (d.percentile < 50 || d.percentile > 99):
		return invalidExperiment("a percentile metric needs percentile, 50 to 99")
	case kind != flags.ExperimentMetricPercentile && d.percentile != 0:
		return invalidExperiment("percentile is only for percentile metrics")
	}
	return nil
}

// experimentRuleWeights checks the rule against the latest configuration and
// returns its flag's variant weights. A bandit's rule must be its flag's
// only rule that allocates by weight, since the bandit rewrites the flag's
// weights.
func experimentRuleWeights(ctx context.Context, store state.FeatureFlagStore, scope state.FeatureFlagScope, flagKey, ruleID, control, unit, allocation string) (map[string]int, *api.Problem) {
	v, err := store.GetFeatureFlags(ctx, scope, 0)
	if err != nil {
		return nil, api.ErrInternal("feature flags unavailable")
	}
	_, f, err := flags.ExperimentRuleForUnit(v.Config, flagKey, ruleID, control, orCustomer(unit))
	if err != nil {
		return nil, invalidExperiment(err.Error())
	}
	if allocation == flags.ExperimentAllocationBandit && flags.WeightedRules(f) != 1 {
		return nil, invalidExperiment("a bandit re-weights its flag's variants, so its rule must be the flag's only rule that allocates by weight")
	}
	return flags.ExperimentWeights(f), nil
}

// noGuardrails turns both guardrails off.
func noGuardrails() flags.GuardrailSettings {
	return flags.GuardrailSettings{ServerErrorRate: flags.GuardrailSetting{Disabled: true}, P95Latency: flags.GuardrailSetting{Disabled: true}}
}

func flagExperimentDesignResponse(out *api.FlagExperiment, x state.FlagExperiment) {
	out.Unit, out.Allocation, out.Layer, out.Traffic = orCustomer(x.Unit), x.Allocation, x.Layer, x.Traffic
	if out.Allocation == "" {
		out.Allocation = flags.ExperimentAllocationFixed
	}
	if x.Layer != "" && x.LayerTo > x.LayerFrom {
		from, to := x.LayerFrom, x.LayerTo
		out.LayerFrom, out.LayerTo = &from, &to
	}
	out.DenominatorEvent, out.Percentile = x.DenominatorEvent, x.Percentile
}
