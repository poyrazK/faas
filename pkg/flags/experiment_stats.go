package flags

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Ratio and percentile metrics, segment adjustment and metric-driven
// rollouts (ADR 20261119173702 §3, §5).

// Further metric kinds.
const (
	ExperimentMetricRatio      = "ratio"
	ExperimentMetricPercentile = "percentile"
)

// CompareRatioMetric runs the mSPRT on the difference of two arms'
// value ratios (numerator value sum over denominator value sum), with
// delta-method variance over units. effect is a relative effect size: the
// mixing standard deviation is effect times the control's ratio.
func CompareRatioMetric(control, variant RatioArm, effect, alpha float64) (Comparison, error) {
	if control.Units < 2 || variant.Units < 2 || control.Den <= 0 || variant.Den <= 0 {
		return Comparison{}, errNoVariance
	}
	tau := effect * math.Abs(control.Ratio())
	if tau <= 0 {
		tau = effect
	}
	return compareNormal(variant.Variant, control.Ratio(), variant.Ratio(), control.ratioVariance()+variant.ratioVariance(), tau, alpha)
}

// Percentile is the p-th percentile (0 < p < 1) of values by linear
// interpolation; 0 with no values.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := min(lo+1, len(sorted)-1)
	return sorted[lo] + (pos-float64(lo))*(sorted[hi]-sorted[lo])
}

// ComparePercentile bootstraps the difference of the arms' p-th
// percentiles: a 95% percentile interval and a two-sided p-value. It has no
// sequential guarantee; callers report it as fixed-horizon.
func ComparePercentile(variant string, control, treated []float64, p float64, resamples int, rng *rand.Rand) (Comparison, error) {
	if len(control) < 2 || len(treated) < 2 || resamples < 2 || p <= 0 || p >= 1 {
		return Comparison{}, errNoVariance
	}
	diffs := make([]float64, resamples)
	bc := make([]float64, len(control))
	bt := make([]float64, len(treated))
	for r := range diffs {
		for i := range bc {
			bc[i] = control[rng.IntN(len(control))]
		}
		for i := range bt {
			bt[i] = treated[rng.IntN(len(treated))]
		}
		diffs[r] = Percentile(bt, p) - Percentile(bc, p)
	}
	slices.Sort(diffs)
	below, above := 0, 0
	for _, d := range diffs {
		if d <= 0 {
			below++
		}
		if d >= 0 {
			above++
		}
	}
	pc := Percentile(control, p)
	c := Comparison{Variant: variant, Lift: Percentile(treated, p) - pc, Low: Percentile(diffs, 0.025), High: Percentile(diffs, 0.975),
		PValue: math.Min(1, 2*float64(min(below, above))/float64(resamples))}
	if pc != 0 {
		c.RelativeLift = c.Lift / math.Abs(pc)
	}
	return c, nil
}

// BenjaminiHochberg returns the adjusted p-values (q-values) of a family of
// tests, in the input order.
func BenjaminiHochberg(p []float64) []float64 {
	n := len(p)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return p[idx[a]] < p[idx[b]] })
	out := make([]float64, n)
	prev := 1.0
	for k := n - 1; k >= 0; k-- {
		i := idx[k]
		q := math.Min(prev, p[i]*float64(n)/float64(k+1))
		out[i] = math.Min(1, q)
		prev = out[i]
	}
	return out
}

// RolloutMetric gates a progressive rollout's stages on a conversion event
// (ADR 20261119173702 §5).
type RolloutMetric struct {
	Event string `json:"event"`
	// MaxDecrease is the absolute conversion rate decrease the stage may
	// cause; 0 is api.RolloutMetricDefaultMaxDecrease.
	MaxDecrease float64 `json:"max_decrease,omitempty"`
	// MinUnits is the customers each group needs before a stage advances;
	// 0 is api.RolloutMetricDefaultMinUnits.
	MinUnits int `json:"min_units,omitempty"`
}

// Limits returns the metric's tolerance and unit floor with defaults.
func (m RolloutMetric) Limits() (maxDecrease float64, minUnits int64) {
	maxDecrease, minUnits = m.MaxDecrease, int64(m.MinUnits)
	if maxDecrease <= 0 {
		maxDecrease = api.RolloutMetricDefaultMaxDecrease
	}
	if minUnits <= 0 {
		minUnits = api.RolloutMetricDefaultMinUnits
	}
	return maxDecrease, minUnits
}

func validateRolloutMetric(r Rule) error {
	m := r.Progression.Metric
	if m == nil {
		return nil
	}
	if !ValidEventName(m.Event) || m.MaxDecrease < 0 || m.MaxDecrease > 1 || m.MinUnits < 0 || m.MinUnits > 1_000_000 {
		return fmt.Errorf("rollout metric needs an event name, max_decrease in [0, 1] and min_units in [0, 1000000]")
	}
	if r.RolloutUnit == "subject" || len(r.Subjects) > 0 {
		return fmt.Errorf("a rollout metric needs a rule that allocates by customer")
	}
	return nil
}

// Rollout metric decisions.
const (
	RolloutAdvance  = "advance"
	RolloutHold     = "hold"
	RolloutRollback = "rollback"
)

// RolloutMetricDecision compares the stage's treatment with the comparison
// group: rollback when the always-valid interval on treatment minus
// comparison lies wholly below -max_decrease; advance when both groups have
// the unit floor and the interval reaches above it; hold otherwise.
func RolloutMetricDecision(m RolloutMetric, comparison, treatment Arm, tau, alpha float64) (string, *Comparison) {
	maxDecrease, minUnits := m.Limits()
	c, err := CompareSequential(comparison, treatment, tau, alpha)
	if err != nil {
		return RolloutHold, nil
	}
	switch {
	case c.High < -maxDecrease:
		return RolloutRollback, &c
	case comparison.Exposed >= minUnits && treatment.Exposed >= minUnits && c.High > -maxDecrease:
		return RolloutAdvance, &c
	}
	return RolloutHold, &c
}
