package flags

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
)

// Experiments compare the variants one weighted rule of a variant flag
// allocates, by the share of exposed customers that later send a conversion
// event (ADR 20261115163113).

// Experiment statuses.
const (
	ExperimentDraft   = "draft"
	ExperimentRunning = "running"
	ExperimentStopped = "stopped"
)

// Metric kinds: conversion compares the share of exposed customers that
// sent the event (ADR 20261115163113); mean compares the mean event value
// per exposed customer (ADR 20261116190208 §2).
const (
	ExperimentMetricConversion = "conversion"
	ExperimentMetricMean       = "mean"
)

// ValidMetricKind reports whether kind is a metric kind.
func ValidMetricKind(kind string) bool {
	switch kind {
	case ExperimentMetricConversion, ExperimentMetricMean, ExperimentMetricFunnel, ExperimentMetricRetention,
		ExperimentMetricRatio, ExperimentMetricPercentile:
		return true
	}
	return false
}

var eventNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// ValidEventName reports whether name is a conversion event name.
func ValidEventName(name string) bool { return eventNamePattern.MatchString(name) }

var experimentKeyPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// ValidExperimentKey reports whether key names an experiment.
func ValidExperimentKey(key string) bool { return experimentKeyPattern.MatchString(key) }

// ExperimentWeights are the variant weights of a flag an experiment reads.
func ExperimentWeights(f Flag) map[string]int {
	out := make(map[string]int, len(f.Variants))
	for _, v := range f.Variants {
		out[v.Key] = v.Weight
	}
	return out
}

// ExperimentRule returns the rule an experiment reads and checks that it can
// carry one: a weighted rule of an enabled variant flag that allocates by
// customer, with control among the flag's variants.
func ExperimentRule(cfg Config, flagKey, ruleID, control string) (Rule, Flag, error) {
	return ExperimentRuleForUnit(cfg, flagKey, ruleID, control, ExperimentUnitCustomer)
}

// ExperimentRuleForUnit is ExperimentRule for customer or subject units: a
// subject experiment needs a rule that allocates by subject
// (ADR 20261119173702 §1).
func ExperimentRuleForUnit(cfg Config, flagKey, ruleID, control, unit string) (Rule, Flag, error) {
	i := slices.IndexFunc(cfg.Flags, func(f Flag) bool { return f.Key == flagKey })
	if i < 0 {
		return Rule{}, Flag{}, fmt.Errorf("flag %q does not exist", flagKey)
	}
	f := cfg.Flags[i]
	if len(f.Variants) < 2 {
		return Rule{}, Flag{}, fmt.Errorf("flag %q is not a variant flag; experiments compare the variants of a weighted rule (make a boolean A/B test a two-variant flag)", flagKey)
	}
	j := slices.IndexFunc(f.Rules, func(r Rule) bool { return r.ID == ruleID })
	if j < 0 {
		return Rule{}, Flag{}, fmt.Errorf("flag %q has no rule %q", flagKey, ruleID)
	}
	r := f.Rules[j]
	if r.Value != nil {
		return Rule{}, Flag{}, fmt.Errorf("rule %q serves one value; experiments need a rule that allocates by variant weight", ruleID)
	}
	bySubject := len(r.Subjects) > 0 || r.RolloutUnit == "subject"
	switch {
	case unit == ExperimentUnitSubject && r.RolloutUnit != "subject":
		return Rule{}, Flag{}, fmt.Errorf("rule %q allocates by customer; a subject experiment needs rollout_unit subject", ruleID)
	case unit != ExperimentUnitSubject && bySubject:
		return Rule{}, Flag{}, fmt.Errorf("rule %q allocates by subject; experiments compare customers unless unit is subject", ruleID)
	}
	if !slices.ContainsFunc(f.Variants, func(v FlagVariant) bool { return v.Key == control }) {
		return Rule{}, Flag{}, fmt.Errorf("control %q is not a variant of flag %q", control, flagKey)
	}
	return r, f, nil
}

// Arm is one variant's exposed and converted units. For mean metrics,
// Converted counts units with a non-zero value and Sum and SumSq are the sum
// and sum of squares of the units' (winsorized) values.
type Arm struct {
	Variant   string  `json:"variant"`
	Exposed   int64   `json:"exposed"`
	Converted int64   `json:"converted"`
	Sum       float64 `json:"sum,omitempty"`
	SumSq     float64 `json:"sum_sq,omitempty"`
}

// Mean is the mean value per exposed unit, 0 with no exposures.
func (a Arm) Mean() float64 {
	if a.Exposed <= 0 {
		return 0
	}
	return a.Sum / float64(a.Exposed)
}

// Variance is the sample variance of the units' values.
func (a Arm) Variance() float64 {
	if a.Exposed < 2 {
		return 0
	}
	n := float64(a.Exposed)
	m := a.Sum / n
	return math.Max(0, (a.SumSq-n*m*m)/(n-1))
}

// CompareMeans runs the mSPRT on the difference of the arms' means. The
// mixing standard deviation is effect times the pooled standard deviation,
// so effect is a standardized effect size (ADR 20261116190208 §2).
func CompareMeans(control, variant Arm, effect, alpha float64) (Comparison, error) {
	if control.Exposed < 2 || variant.Exposed < 2 {
		return Comparison{}, errNoVariance
	}
	vc, vv := control.Variance(), variant.Variance()
	nc, nv := float64(control.Exposed), float64(variant.Exposed)
	pooled := ((nc-1)*vc + (nv-1)*vv) / (nc + nv - 2)
	v := vc/nc + vv/nv
	return compareNormal(variant.Variant, control.Mean(), variant.Mean(), v, effect*math.Sqrt(pooled), alpha)
}

// Rate is the conversion rate, 0 with no exposures.
func (a Arm) Rate() float64 {
	if a.Exposed <= 0 {
		return 0
	}
	return float64(a.Converted) / float64(a.Exposed)
}

// Comparison is one variant against control under the mixture sequential
// probability ratio test.
type Comparison struct {
	Variant      string  `json:"variant"`
	Lift         float64 `json:"lift"`          // absolute: rate(variant) - rate(control)
	RelativeLift float64 `json:"relative_lift"` // lift / rate(control); 0 when control converts nothing
	Low          float64 `json:"low"`           // confidence sequence bounds on Lift
	High         float64 `json:"high"`
	PValue       float64 `json:"p_value"` // always-valid; the caller keeps the running minimum
}

var errNoVariance = errors.New("no variance yet")

// CompareSequential runs the normal-approximation mSPRT for the difference
// of two proportions with mixing standard deviation tau at level alpha
// (Johari et al. 2017). It returns errNoVariance while either arm has no
// exposures or the pooled variance is zero.
func CompareSequential(control, variant Arm, tau, alpha float64) (Comparison, error) {
	if control.Exposed <= 0 || variant.Exposed <= 0 {
		return Comparison{}, errNoVariance
	}
	pc, pv := control.Rate(), variant.Rate()
	v := pc*(1-pc)/float64(control.Exposed) + pv*(1-pv)/float64(variant.Exposed)
	return compareNormal(variant.Variant, pc, pv, v, tau, alpha)
}

// compareNormal is the mSPRT for a difference of two estimates whose
// sampling variance is v, with a normal mixture of standard deviation tau
// over the true difference. Low and High are the always-valid confidence
// sequence at level alpha.
func compareNormal(variant string, control, treated, v, tau, alpha float64) (Comparison, error) {
	if !(v > 0) || !(tau > 0) || alpha <= 0 || alpha >= 1 || math.IsInf(v, 0) {
		return Comparison{}, errNoVariance
	}
	t2 := tau * tau
	delta := treated - control
	logLambda := 0.5*math.Log(v/(v+t2)) + t2*delta*delta/(2*v*(v+t2))
	p := 1.0
	if logLambda > 0 {
		p = math.Exp(-logLambda)
	}
	half := math.Sqrt((v * (v + t2) / t2) * (math.Log((v+t2)/v) + 2*math.Log(1/alpha)))
	c := Comparison{Variant: variant, Lift: delta, Low: delta - half, High: delta + half, PValue: math.Min(1, p)}
	if control != 0 {
		c.RelativeLift = delta / math.Abs(control)
	}
	return c, nil
}

// SampleRatioPValue is the chi-square goodness-of-fit p-value of exposed
// units against the rule's configured weights. A small value means the
// allocation the app observed is not the one configured (a sample ratio
// mismatch), so results are not trustworthy. Arms with zero weight are
// ignored; it returns 1 with fewer than two weighted arms or no exposures.
func SampleRatioPValue(arms []Arm, weights map[string]int) float64 {
	var total, weightSum float64
	used := 0
	for _, a := range arms {
		if w := weights[a.Variant]; w > 0 {
			total += float64(a.Exposed)
			weightSum += float64(w)
			used++
		}
	}
	if used < 2 || total <= 0 {
		return 1
	}
	var chi float64
	for _, a := range arms {
		w := weights[a.Variant]
		if w <= 0 {
			continue
		}
		expected := total * float64(w) / weightSum
		d := float64(a.Exposed) - expected
		chi += d * d / expected
	}
	return chiSquareSurvival(chi, float64(used-1))
}

// chiSquareSurvival is P(X >= x) for X ~ chi-square(k).
func chiSquareSurvival(x, k float64) float64 {
	if x <= 0 {
		return 1
	}
	return upperRegularizedGamma(k/2, x/2)
}

// upperRegularizedGamma is Q(a, x) by series for x < a+1 and a continued
// fraction otherwise (Numerical Recipes 6.2).
func upperRegularizedGamma(a, x float64) float64 {
	lg, _ := math.Lgamma(a)
	if x < a+1 {
		sum, term := 1/a, 1/a
		for n := 1; n < 500; n++ {
			term *= x / (a + float64(n))
			sum += term
			if math.Abs(term) < math.Abs(sum)*1e-15 {
				break
			}
		}
		return math.Max(0, 1-sum*math.Exp(-x+a*math.Log(x)-lg))
	}
	const tiny = 1e-300
	b := x + 1 - a
	c := 1 / tiny
	d := 1 / b
	h := d
	for i := 1; i < 500; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-15 {
			break
		}
	}
	return math.Min(1, math.Exp(-x+a*math.Log(x)-lg)*h)
}

// Result statuses, in the order results are checked.
const (
	ExperimentSampleRatioMismatch = "sample_ratio_mismatch"
	ExperimentInsufficientData    = "insufficient_data"
	ExperimentSignificant         = "significant"
	ExperimentNotSignificant      = "not_significant"
)

// ResultsInput is everything ExperimentResults needs.
type ResultsInput struct {
	Arms           []Arm
	Control        string
	Weights        map[string]int
	MixingLift     float64
	Alpha          float64
	SRMAlpha       float64
	MinUnits       int64
	MinConversions int64
	// MinPValues are the running minimum p-values the reconciler stored per
	// variant; the always-valid p-value is the minimum over time.
	MinPValues map[string]float64
	// Kind is the metric kind; empty is conversion.
	Kind string
	// Cuped are the per-variant CUPED sums, or nil when CUPED does not
	// apply, with CupedReason saying why (ADR 20261119102741 §2).
	Cuped       []CupedArm
	CupedReason string
	// MinCupedPValues are the running minimum adjusted p-values.
	MinCupedPValues map[string]float64
}

// VariantResult is one variant compared with control. Rate is the
// conversion rate, or the mean value per unit for mean metrics.
type VariantResult struct {
	Arm
	Rate       float64     `json:"rate"`
	Comparison *Comparison `json:"comparison,omitempty"`
	// Unadjusted is the comparison without CUPED when Comparison is
	// adjusted.
	Unadjusted *Comparison `json:"unadjusted,omitempty"`
	Status     string      `json:"status"`
}

// Results is an experiment's report. ControlRate is the control's
// conversion rate, or its mean value per unit for mean metrics.
type Results struct {
	Control          Arm             `json:"control"`
	ControlRate      float64         `json:"control_rate"`
	Variants         []VariantResult `json:"variants"`
	SampleRatioP     float64         `json:"sample_ratio_p_value"`
	SampleRatioError bool            `json:"sample_ratio_mismatch"`
	Cuped            CupedSummary    `json:"cuped"`
}

// cupedTau is the mixing scale of an adjusted comparison: the same as the
// unadjusted one's.
func cupedTau(in ResultsInput, control, variant Arm) float64 {
	if in.Kind != ExperimentMetricMean {
		return in.MixingLift
	}
	nc, nv := float64(control.Exposed), float64(variant.Exposed)
	if nc+nv <= 2 {
		return 0
	}
	pooled := ((nc-1)*control.Variance() + (nv-1)*variant.Variance()) / (nc + nv - 2)
	return in.MixingLift * math.Sqrt(pooled)
}

// cupedSetup decides whether CUPED applies and returns the arms by variant.
func cupedSetup(in ResultsInput) (CupedSummary, map[string]CupedArm, float64) {
	if in.Cuped == nil {
		reason := in.CupedReason
		if reason == "" {
			reason = CupedOff
		}
		return CupedSummary{Reason: reason}, nil, 0
	}
	theta, rho2, meanX, ok := CupedTheta(in.Cuped)
	if !ok {
		return CupedSummary{Reason: CupedNoVariance}, nil, 0
	}
	byVariant := map[string]CupedArm{}
	for _, a := range in.Cuped {
		byVariant[a.Variant] = a
	}
	return CupedSummary{Applied: true, Theta: theta, VarianceReduction: rho2}, byVariant, meanX
}

// ExperimentResults compares every non-control arm with control. A sample
// ratio mismatch makes every variant sample_ratio_mismatch; an arm below
// the unit or conversion floor is insufficient_data; otherwise a variant is
// significant when its always-valid p-value is below alpha.
func ExperimentResults(in ResultsInput) Results {
	byVariant := map[string]Arm{}
	for _, a := range in.Arms {
		byVariant[a.Variant] = a
	}
	// A weighted variant nobody was exposed to is an arm with zero units.
	all := make([]Arm, 0, len(in.Weights))
	for v := range in.Weights {
		a := byVariant[v]
		a.Variant = v
		all = append(all, a)
	}
	out := Results{SampleRatioP: SampleRatioPValue(all, in.Weights)}
	out.SampleRatioError = out.SampleRatioP < in.SRMAlpha
	control := byVariant[in.Control]
	control.Variant = in.Control
	value := Arm.Rate
	compare := CompareSequential
	if in.Kind == ExperimentMetricMean {
		value, compare = Arm.Mean, CompareMeans
	}
	out.Control, out.ControlRate = control, value(control)
	variants := make([]string, 0, len(in.Weights))
	for v := range in.Weights {
		if v != in.Control {
			variants = append(variants, v)
		}
	}
	slices.Sort(variants)
	enough := func(a Arm) bool { return a.Exposed >= in.MinUnits && a.Converted >= in.MinConversions }
	var cupedArms map[string]CupedArm
	var meanX float64
	out.Cuped, cupedArms, meanX = cupedSetup(in)
	for _, v := range variants {
		arm := byVariant[v]
		arm.Variant = v
		r := VariantResult{Arm: arm, Rate: value(arm)}
		if c, err := compare(control, arm, in.MixingLift, in.Alpha); err == nil {
			if prev, ok := in.MinPValues[v]; ok && prev < c.PValue {
				c.PValue = prev
			}
			r.Comparison = &c
		}
		if cupedArms != nil {
			cc, err := CompareCuped(cupedArms[in.Control], cupedArms[v], out.Cuped.Theta, meanX, cupedTau(in, control, arm), in.Alpha)
			if err == nil {
				if prev, ok := in.MinCupedPValues[v]; ok && prev < cc.PValue {
					cc.PValue = prev
				}
				r.Unadjusted, r.Comparison = r.Comparison, &cc
			}
		}
		switch {
		case out.SampleRatioError:
			r.Status = ExperimentSampleRatioMismatch
		case !enough(control) || !enough(arm) || r.Comparison == nil:
			r.Status = ExperimentInsufficientData
		case r.Comparison.PValue < in.Alpha:
			r.Status = ExperimentSignificant
		default:
			r.Status = ExperimentNotSignificant
		}
		out.Variants = append(out.Variants, r)
	}
	return out
}
