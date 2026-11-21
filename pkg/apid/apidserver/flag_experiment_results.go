package apidserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Ratio and percentile results, bandit posteriors and segments (ADR
// 20261119173702 §3–4).

// flagExperimentExtras is what ratio and percentile metrics read beyond
// the arms.
type flagExperimentExtras struct {
	ratio  []flags.RatioArm
	values map[string][]float64
}

func loadFlagExperimentExtras(ctx context.Context, store state.FlagExperimentStore, x state.FlagExperiment) (flagExperimentExtras, error) {
	var out flagExperimentExtras
	es, ok := store.(state.FlagExperimentExtrasStore)
	if !ok {
		return out, nil
	}
	var err error
	switch x.MetricKind {
	case flags.ExperimentMetricRatio:
		out.ratio, err = es.FlagExperimentRatioArms(ctx, x)
	case flags.ExperimentMetricPercentile:
		out.values, err = es.FlagExperimentUnitValues(ctx, x, api.ExperimentPercentileMaxUnits)
	}
	return out, err
}

// customMetric reports whether kind's comparison is not one of
// ExperimentResults' own: ratio and percentile metrics.
func customMetric(kind string) bool {
	return kind == flags.ExperimentMetricRatio || kind == flags.ExperimentMetricPercentile
}

// flagExperimentResultsFor applies the statistics of the experiment's
// metric kind. Ratio and percentile metrics take ExperimentResults' counts
// and sample-ratio check and replace each comparison with their own.
func flagExperimentResultsFor(x state.FlagExperiment, arms []flags.Arm, cuped flagExperimentCupedInput, ex flagExperimentExtras) flags.Results {
	if !customMetric(x.MetricKind) {
		return flagExperimentResults(x, arms, cuped)
	}
	base := x
	base.MetricKind = flags.ExperimentMetricConversion
	res := flagExperimentResults(base, arms, cuped)
	ratios := map[string]flags.RatioArm{}
	for _, a := range ex.ratio {
		ratios[a.Variant] = a
	}
	p := float64(x.Percentile) / 100
	for i := range res.Variants {
		v := &res.Variants[i]
		v.Unadjusted, v.Comparison = nil, nil
		var c flags.Comparison
		var err error
		if x.MetricKind == flags.ExperimentMetricRatio {
			c, err = flags.CompareRatioMetric(ratios[x.Control], ratios[v.Variant], x.MixingLift, api.FlagExperimentAlpha)
			if prev, ok := x.MinPValues[v.Variant]; ok && err == nil && prev < c.PValue {
				c.PValue = prev
			}
		} else {
			c, err = flags.ComparePercentile(v.Variant, ex.values[x.Control], ex.values[v.Variant], p,
				api.ExperimentPercentileResamples, flags.BanditRNG(x.ID, 0))
		}
		if err == nil {
			v.Comparison = &c
		}
		v.Status = customStatus(res.SampleRatioError, res.Control.Exposed, v.Exposed, v.Comparison)
	}
	return res
}

func customStatus(srm bool, control, variant int64, c *flags.Comparison) string {
	switch {
	case srm:
		return flags.ExperimentSampleRatioMismatch
	case control < api.FlagExperimentMinUnitsPerArm || variant < api.FlagExperimentMinUnitsPerArm || c == nil:
		return flags.ExperimentInsufficientData
	case c.PValue < api.FlagExperimentAlpha:
		return flags.ExperimentSignificant
	}
	return flags.ExperimentNotSignificant
}

// customArmValue is a ratio arm's ratio or a percentile arm's percentile.
func customArmValue(x state.FlagExperiment, ex flagExperimentExtras, variant string) float64 {
	if x.MetricKind == flags.ExperimentMetricRatio {
		for _, a := range ex.ratio {
			if a.Variant == variant {
				return a.Ratio()
			}
		}
		return 0
	}
	return flags.Percentile(ex.values[variant], float64(x.Percentile)/100)
}

// banditResponse is a bandit's stored posterior with the weights the rule
// serves now.
func banditResponse(x state.FlagExperiment) *api.FlagExperimentBandit {
	if x.Allocation != flags.ExperimentAllocationBandit {
		return nil
	}
	out := &api.FlagExperimentBandit{Arms: make([]api.FlagExperimentBanditArm, 0, len(x.Bandit)), WeightedAt: x.BanditWeightedAt}
	for _, a := range x.Bandit {
		out.Arms = append(out.Arms, api.FlagExperimentBanditArm{Variant: a.Variant, Weight: x.Weights[a.Variant],
			BestProbability: a.BestProbability, ExpectedLoss: a.ExpectedLoss})
	}
	return out
}

// banditShip is a bandit's recommendation: ship an arm once it is best
// with probability ExperimentBanditShipProbability and choosing it gives up
// less than ExperimentBanditShipLoss.
func banditShip(x state.FlagExperiment) (string, bool) {
	for _, a := range x.Bandit {
		if a.BestProbability >= api.ExperimentBanditShipProbability && a.ExpectedLoss < api.ExperimentBanditShipLoss {
			return a.Variant, true
		}
	}
	return "", false
}

// banditRecommendation replaces a bandit's p-value recommendation.
func banditRecommendation(x state.FlagExperiment) flags.Recommendation {
	if v, ok := banditShip(x); ok {
		return flags.Recommendation{Action: flags.RecommendShipVariant, Variant: v,
			Summary: "the bandit finds " + v + " best with high probability and little expected loss"}
	}
	if x.Status != flags.ExperimentRunning {
		return flags.Recommendation{Action: flags.RecommendDecide, Summary: "the bandit stopped before any arm was clearly best"}
	}
	return flags.Recommendation{Action: flags.RecommendKeepRunning, Summary: "no arm is clearly best yet; the bandit keeps shifting traffic to the leaders"}
}

// segmentResults splits a binary experiment by a cohort: its members and
// everyone else, each variant's p-value adjusted with Benjamini–Hochberg
// over every segment comparison.
func segmentResults(x state.FlagExperiment, cohort string, members, others []flags.Arm) []api.FlagExperimentSegment {
	type cell struct{ seg, variant int }
	var ps []float64
	var cells []cell
	out := make([]api.FlagExperimentSegment, 0, 2)
	for i, arms := range [][]flags.Arm{members, others} {
		res := flags.ExperimentResults(flags.ResultsInput{Arms: arms, Control: x.Control, Weights: x.Weights, MixingLift: x.MixingLift,
			Alpha: api.FlagExperimentAlpha, SRMAlpha: api.FlagExperimentSRMAlpha, MinUnits: api.FlagExperimentMinUnitsPerArm,
			MinConversions: api.FlagExperimentMinConversionsPerArm, Kind: flags.ExperimentMetricConversion, CupedReason: flags.CupedOff})
		seg := api.FlagExperimentSegment{Cohort: cohort, Members: i == 0, Control: rateArm(res.Control),
			Variants: make([]api.FlagExperimentVariantResult, 0, len(res.Variants))}
		for j, v := range res.Variants {
			vr := api.FlagExperimentVariantResult{FlagExperimentArm: rateArm(v.Arm), Status: v.Status}
			if c := v.Comparison; c != nil {
				lift, rel, lo, hi, p := c.Lift, c.RelativeLift, c.Low, c.High, c.PValue
				vr.Lift, vr.RelativeLift, vr.Low, vr.High, vr.PValue = &lift, &rel, &lo, &hi, &p
				ps = append(ps, p)
				cells = append(cells, cell{i, j})
			}
			seg.Variants = append(seg.Variants, vr)
		}
		out = append(out, seg)
	}
	adjusted := flags.BenjaminiHochberg(ps)
	for k, c := range cells {
		vr := &out[c.seg].Variants[c.variant]
		q := adjusted[k]
		vr.PValue = &q
		if vr.Status == flags.ExperimentSignificant && q >= api.ExperimentSegmentFDR {
			vr.Status = flags.ExperimentNotSignificant
		}
		if vr.Status == flags.ExperimentNotSignificant && q < api.ExperimentSegmentFDR {
			vr.Status = flags.ExperimentSignificant
		}
	}
	return out
}

func rateArm(a flags.Arm) api.FlagExperimentArm {
	return api.FlagExperimentArm{Variant: a.Variant, Exposed: a.Exposed, Converted: a.Converted, Rate: a.Rate()}
}

// banditStep recomputes a bandit's posterior and, at most every
// ExperimentBanditInterval and only when the weights move at least
// ExperimentBanditMinShiftBasisPoints, publishes new variant weights.
func (r *flagExperimentReconciler) banditStep(ctx context.Context, x state.FlagExperiment, now time.Time) error {
	es, ok := r.store.(state.FlagExperimentExtrasStore)
	if !ok || x.Allocation != flags.ExperimentAllocationBandit {
		return nil
	}
	arms, _, err := r.store.FlagExperimentArms(ctx, x, api.FlagExperimentMinUnitsPerArm)
	if err != nil {
		return err
	}
	all := make([]flags.Arm, 0, len(x.Weights))
	for v := range x.Weights {
		i := slices.IndexFunc(arms, func(a flags.Arm) bool { return a.Variant == v })
		a := flags.Arm{Variant: v}
		if i >= 0 {
			a = arms[i]
		}
		all = append(all, a)
	}
	slices.SortFunc(all, func(a, b flags.Arm) int { return cmpString(a.Variant, b.Variant) })
	posterior := flags.BanditPosterior(all, api.ExperimentBanditSamples, flags.BanditRNG(x.ID, now.Unix()/int64(flagExperimentInterval/time.Second)))
	due := x.BanditWeightedAt == nil || now.Sub(*x.BanditWeightedAt) >= api.ExperimentBanditInterval
	weights := flags.BanditWeights(posterior, api.ExperimentBanditFloorBasisPoints)
	enough := true
	for _, a := range all {
		if a.Exposed < api.FlagExperimentMinUnitsPerArm {
			enough = false
		}
	}
	if !due || !enough || flags.BanditShift(x.Weights, weights) < api.ExperimentBanditMinShiftBasisPoints {
		return es.SetFlagExperimentBandit(ctx, x.ID, posterior, nil, nil)
	}
	_, err = r.server.publishFlags(ctx, experimentScope(x), flagExperimentBanditActor, func(cfg flags.Config) (flags.Config, error) {
		next, ok := flags.SetVariantWeights(cfg, x.FlagKey, weights)
		if !ok {
			return cfg, errFlagExperimentRuleChanged
		}
		return next, nil
	})
	if err != nil {
		if serr := es.SetFlagExperimentBandit(ctx, x.ID, posterior, nil, nil); serr != nil {
			return serr
		}
		return err
	}
	at := now
	return es.SetFlagExperimentBandit(ctx, x.ID, posterior, weights, &at)
}

// flagExperimentBanditActor publishes a bandit's re-weighting.
const flagExperimentBanditActor = "system:flags-experiment-bandit"

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// banditVariants reports each arm's posterior instead of p-values: a
// bandit's allocation moves on purpose, so neither its p-values nor its
// sample-ratio check hold.
func banditVariants(out *api.FlagExperimentResults) {
	for i := range out.Variants {
		v := &out.Variants[i]
		v.Lift, v.RelativeLift, v.Low, v.High, v.PValue, v.Unadjusted = nil, nil, nil, nil, nil, nil
		for _, a := range out.Bandit.Arms {
			if a.Variant == v.Variant {
				bp, el := a.BestProbability, a.ExpectedLoss
				v.BestProbability, v.ExpectedLoss = &bp, &el
			}
		}
	}
	out.SampleRatioPValue, out.SampleRatioMismatch = 0, false
}

// flagExperimentSegments splits a binary customer experiment's results by
// a cohort of the environment.
func (s *server) flagExperimentSegments(ctx context.Context, store state.FlagExperimentStore, x state.FlagExperiment, key string) ([]api.FlagExperimentSegment, *api.Problem) {
	if !flags.BinaryMetric(x.MetricKind) || x.SubjectUnits() {
		return nil, invalidExperiment("segments split binary metrics of customer experiments")
	}
	cohorts, ok1 := s.store.(state.AnalyticsCohortStore)
	es, ok2 := store.(state.FlagExperimentExtrasStore)
	if !ok1 || !ok2 {
		return nil, api.ErrInternal("product analytics unavailable")
	}
	c, err := cohorts.GetAnalyticsCohort(ctx, x.EnvironmentID, key)
	if errors.Is(err, state.ErrNotFound) {
		return nil, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Cohort not found", "no cohort of this environment has that key")
	}
	if err != nil {
		return nil, api.ErrInternal("product analytics unavailable")
	}
	members, others, err := es.FlagExperimentSegmentArms(ctx, x, c.ID)
	if err != nil {
		return nil, api.ErrInternal("flag experiments unavailable")
	}
	return segmentResults(x, c.Key, members, others), nil
}
