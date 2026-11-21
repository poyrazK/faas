package flags

import (
	"math"
	"math/rand/v2"
	"testing"
)

// ADR 20261119102741 §2: CUPED keeps the estimate unbiased and narrows the
// confidence sequence when the covariate predicts the metric.
func TestCupedNarrowsIntervalWithPredictiveCovariate(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	build := func(variant string, lift float64, n int) (Arm, CupedArm) {
		a, c := Arm{Variant: variant}, CupedArm{Variant: variant}
		for range n {
			x := rng.NormFloat64()*10 + 50
			y := 0.8*x + rng.NormFloat64()*3 + lift
			a.Exposed++
			if y != 0 {
				a.Converted++
			}
			a.Sum += y
			a.SumSq += y * y
			c.N++
			c.SumY += y
			c.SumY2 += y * y
			c.SumX += x
			c.SumX2 += x * x
			c.SumXY += x * y
		}
		return a, c
	}
	ac, cc := build("control", 0, 4000)
	av, cv := build("new", 1, 4000)
	in := ResultsInput{
		Arms: []Arm{ac, av}, Control: "control", Weights: map[string]int{"control": 50, "new": 50},
		MixingLift: 0.1, Alpha: 0.05, SRMAlpha: 0.001, MinUnits: 100, Kind: ExperimentMetricMean,
	}
	plain := ExperimentResults(in)
	in.Cuped = []CupedArm{cc, cv}
	adjusted := ExperimentResults(in)
	if !adjusted.Cuped.Applied || adjusted.Cuped.VarianceReduction < 0.5 {
		t.Fatalf("cuped = %+v; want applied with a large variance reduction", adjusted.Cuped)
	}
	p, a := plain.Variants[0], adjusted.Variants[0]
	if a.Unadjusted == nil || math.Abs(a.Unadjusted.Lift-p.Comparison.Lift) > 1e-9 {
		t.Fatalf("unadjusted comparison %+v; want the plain one %+v", a.Unadjusted, p.Comparison)
	}
	if width := a.Comparison.High - a.Comparison.Low; width >= 0.6*(p.Comparison.High-p.Comparison.Low) {
		t.Fatalf("adjusted width %.3f not much narrower than %.3f", width, p.Comparison.High-p.Comparison.Low)
	}
	if math.Abs(a.Comparison.Lift-1) > 0.3 {
		t.Fatalf("adjusted lift %.3f; want about 1", a.Comparison.Lift)
	}
	if a.Status != ExperimentSignificant {
		t.Fatalf("status %s; want significant", a.Status)
	}
}

func TestCupedFallsBackWithoutCovariateVariance(t *testing.T) {
	arms := []Arm{{Variant: "control", Exposed: 200, Converted: 40}, {Variant: "new", Exposed: 200, Converted: 60}}
	flat := []CupedArm{
		{Variant: "control", N: 200, SumY: 40, SumY2: 40, SumX: 200, SumX2: 200, SumXY: 40},
		{Variant: "new", N: 200, SumY: 60, SumY2: 60, SumX: 200, SumX2: 200, SumXY: 60},
	}
	res := ExperimentResults(ResultsInput{Arms: arms, Control: "control", Weights: map[string]int{"control": 1, "new": 1},
		MixingLift: 0.05, Alpha: 0.05, SRMAlpha: 0.001, Cuped: flat})
	if res.Cuped.Applied || res.Cuped.Reason != CupedNoVariance || res.Variants[0].Unadjusted != nil {
		t.Fatalf("cuped = %+v, unadjusted = %v; want not applied for a constant covariate", res.Cuped, res.Variants[0].Unadjusted)
	}
	res = ExperimentResults(ResultsInput{Arms: arms, Control: "control", Weights: map[string]int{"control": 1, "new": 1},
		MixingLift: 0.05, Alpha: 0.05, SRMAlpha: 0.001, CupedReason: CupedPending})
	if res.Cuped.Reason != CupedPending {
		t.Fatalf("reason = %q; want %q", res.Cuped.Reason, CupedPending)
	}
}

func TestValidateMetricDefinition(t *testing.T) {
	key := func(k string) bool { return k != "" && k != "Bad" }
	steps := []MetricStep{{Event: "signup"}, {Event: "order_completed", Where: map[string]string{"plan": "pro"}}}
	ret := &MetricStep{Event: "session_start"}
	for _, tc := range []struct {
		name string
		kind string
		def  MetricDefinition
		ok   bool
	}{
		{"funnel", ExperimentMetricFunnel, MetricDefinition{Steps: steps, Window: "72h"}, true},
		{"funnel one step", ExperimentMetricFunnel, MetricDefinition{Steps: steps[:1], Window: "72h"}, false},
		{"funnel short window", ExperimentMetricFunnel, MetricDefinition{Steps: steps, Window: "10m"}, false},
		{"funnel bad filter", ExperimentMetricFunnel, MetricDefinition{Steps: []MetricStep{{Event: "a"}, {Event: "b", Where: map[string]string{"Bad": "x"}}}, Window: "1h"}, false},
		{"funnel with period", ExperimentMetricFunnel, MetricDefinition{Steps: steps, Window: "72h", Period: PeriodDay}, false},
		{"retention", ExperimentMetricRetention, MetricDefinition{Return: ret, Period: PeriodWeek, PeriodNumber: 1}, true},
		{"retention no return", ExperimentMetricRetention, MetricDefinition{Period: PeriodDay, PeriodNumber: 7}, false},
		{"retention bad period", ExperimentMetricRetention, MetricDefinition{Return: ret, Period: "month", PeriodNumber: 1}, false},
		{"retention period 13", ExperimentMetricRetention, MetricDefinition{Return: ret, Period: PeriodDay, PeriodNumber: 13}, false},
		{"conversion plain", ExperimentMetricConversion, MetricDefinition{}, true},
		{"conversion with steps", ExperimentMetricConversion, MetricDefinition{Steps: steps}, false},
	} {
		err := ValidateMetricDefinition(tc.kind, tc.def, key)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v; want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// ADR 20261119102741 §4: gregale.cohort matches any listed cohort; a
// customer in none matches not_in; a missing header matches nothing.
func TestCohortConditions(t *testing.T) {
	in := []Condition{{Attribute: AttributeCohort, Op: OpIn, Values: []string{"onboarded", "power"}}}
	notIn := []Condition{{Attribute: AttributeCohort, Op: OpNotIn, Values: []string{"churned"}}}
	for _, tc := range []struct {
		name  string
		attrs map[string]string
		in    bool
		notIn bool
	}{
		{"member", map[string]string{AttributeCohort: "beta,onboarded"}, true, true},
		{"other cohorts", map[string]string{AttributeCohort: "beta"}, false, true},
		{"in churned", map[string]string{AttributeCohort: "churned"}, false, false},
		{"none", map[string]string{AttributeCohort: CohortsNone}, false, true},
		{"missing", map[string]string{}, false, false},
		{"malformed", map[string]string{AttributeCohort: "Bad Key"}, false, false},
	} {
		if got := MatchConditions(in, tc.attrs); got != tc.in {
			t.Errorf("%s: in = %v; want %v", tc.name, got, tc.in)
		}
		if got := MatchConditions(notIn, tc.attrs); got != tc.notIn {
			t.Errorf("%s: not_in = %v; want %v", tc.name, got, tc.notIn)
		}
	}
	if FormatCohorts([]string{"power", "beta", "power"}) != "beta,power" || FormatCohorts(nil) != CohortsNone {
		t.Fatal("FormatCohorts does not sort, dedupe or mark none")
	}
	if err := validateConditions([]Condition{{Attribute: AttributeCohort, Op: OpEndsWith, Values: []string{"a"}}}); err == nil {
		t.Fatal("ends_with on gregale.cohort accepted")
	}
	if err := validateConditions([]Condition{{Attribute: AttributeCohort, Op: OpIn, Values: []string{"Not-A-Key"}}}); err == nil {
		t.Fatal("invalid cohort key accepted")
	}
	if err := validateConditions(in); err != nil {
		t.Fatalf("valid cohort condition refused: %v", err)
	}
}
