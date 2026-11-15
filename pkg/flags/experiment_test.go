package flags

import (
	"math"
	"strings"
	"testing"
)

func TestChiSquareSurvivalKnownCriticalValues(t *testing.T) {
	for _, tc := range []struct {
		x, k, want float64
	}{
		{3.841458820694124, 1, 0.05},
		{6.634896601021214, 1, 0.01},
		{5.991464547107979, 2, 0.05},
		{11.344866730144373, 3, 0.01},
		{0.4549364231195724, 1, 0.5},
		{30.577914166892505, 15, 0.01},
		{0, 2, 1},
	} {
		if got := chiSquareSurvival(tc.x, tc.k); math.Abs(got-tc.want) > 1e-6 {
			t.Errorf("chiSquareSurvival(%v, %v) = %v; want %v", tc.x, tc.k, got, tc.want)
		}
	}
}

func TestSampleRatioPValue(t *testing.T) {
	even := map[string]int{"a": 50, "b": 50}
	for _, tc := range []struct {
		name    string
		arms    []Arm
		weights map[string]int
		low     bool
	}{
		{"matches the weights", []Arm{{Variant: "a", Exposed: 1000}, {Variant: "b", Exposed: 1000}}, even, false},
		{"small noise", []Arm{{Variant: "a", Exposed: 1000}, {Variant: "b", Exposed: 1040}}, even, false},
		{"mismatch", []Arm{{Variant: "a", Exposed: 1000}, {Variant: "b", Exposed: 1200}}, even, true},
		{"uneven weights matched", []Arm{{Variant: "a", Exposed: 900}, {Variant: "b", Exposed: 100}}, map[string]int{"a": 90, "b": 10}, false},
		{"one weighted arm", []Arm{{Variant: "a", Exposed: 900}, {Variant: "b", Exposed: 100}}, map[string]int{"a": 100}, false},
		{"no exposures", []Arm{{Variant: "a"}, {Variant: "b"}}, even, false},
	} {
		p := SampleRatioPValue(tc.arms, tc.weights)
		if got := p < 0.001; got != tc.low {
			t.Errorf("%s: p = %v; want below 0.001 = %v", tc.name, p, tc.low)
		}
	}
}

func TestCompareSequential(t *testing.T) {
	control := Arm{Variant: "off", Exposed: 5000, Converted: 500}
	same := Arm{Variant: "on", Exposed: 5000, Converted: 505}
	better := Arm{Variant: "on", Exposed: 5000, Converted: 650}

	c, err := CompareSequential(control, same, 0.02, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if c.PValue < 0.5 || c.Low > 0 || c.High < 0 {
		t.Errorf("no real difference: %+v; want a high p-value and an interval around 0", c)
	}
	c, err = CompareSequential(control, better, 0.02, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if c.PValue > 0.001 || c.Low <= 0 || math.Abs(c.Lift-0.03) > 1e-12 || math.Abs(c.RelativeLift-0.3) > 1e-12 {
		t.Errorf("3-point lift: %+v; want a tiny p-value, an interval above 0, lift 0.03 and relative lift 0.3", c)
	}
	if _, err := CompareSequential(Arm{Variant: "off"}, better, 0.02, 0.05); err == nil {
		t.Error("an arm with no exposures has no variance")
	}
	if _, err := CompareSequential(Arm{Variant: "off", Exposed: 10}, Arm{Variant: "on", Exposed: 10}, 0.02, 0.05); err == nil {
		t.Error("no conversions anywhere has no variance")
	}
}

// The confidence sequence and the p-value invert the same test: 0 lies
// outside the interval exactly when the p-value is below alpha.
func TestCompareSequentialIntervalMatchesPValue(t *testing.T) {
	const alpha = 0.05
	for n := int64(50); n <= 20000; n = n*3/2 + 7 {
		for _, rates := range [][2]float64{{0.1, 0.1}, {0.1, 0.12}, {0.2, 0.17}, {0.05, 0.09}, {0.5, 0.52}} {
			c, err := CompareSequential(
				Arm{Variant: "c", Exposed: n, Converted: int64(rates[0] * float64(n))},
				Arm{Variant: "v", Exposed: n, Converted: int64(rates[1] * float64(n))}, 0.02, alpha)
			if err != nil {
				continue
			}
			excludesZero := c.Low > 0 || c.High < 0
			if excludesZero != (c.PValue < alpha) && math.Abs(c.PValue-alpha) > 1e-9 {
				t.Errorf("n=%d rates=%v: interval [%v, %v] and p=%v disagree", n, rates, c.Low, c.High, c.PValue)
			}
		}
	}
}

// Peeking after every batch of an A/A test must not inflate false positives
// beyond alpha: the always-valid property, checked by simulation with a
// fixed-seed generator.
func TestCompareSequentialAAFalsePositiveRate(t *testing.T) {
	const runs, batches, perBatch, alpha = 400, 40, 100, 0.05
	rng := uint64(0x9e3779b97f4a7c15)
	next := func() float64 {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return float64(rng>>11) / (1 << 53)
	}
	falsePositives := 0
	for r := 0; r < runs; r++ {
		a, b := Arm{Variant: "a"}, Arm{Variant: "b"}
		for i := 0; i < batches; i++ {
			for j := 0; j < perBatch; j++ {
				a.Exposed++
				b.Exposed++
				if next() < 0.1 {
					a.Converted++
				}
				if next() < 0.1 {
					b.Converted++
				}
			}
			if c, err := CompareSequential(a, b, 0.02, alpha); err == nil && c.PValue < alpha {
				falsePositives++
				break
			}
		}
	}
	if rate := float64(falsePositives) / runs; rate > alpha {
		t.Errorf("A/A false positive rate with peeking = %v; want at most %v", rate, alpha)
	}
}

func TestExperimentRule(t *testing.T) {
	half := 50
	cfg := Config{Flags: []Flag{
		{Key: "checkout", Enabled: true, Default: "legacy", Variants: []FlagVariant{{Key: "legacy", Weight: 50}, {Key: "new", Weight: 50}},
			Rules: []Rule{
				{ID: "weighted"},
				{ID: "fixed", Value: "new"},
				{ID: "by-subject", RolloutUnit: "subject", Rollout: &half},
			}},
		{Key: "export", Enabled: true, Default: false, Rules: []Rule{{ID: "everyone-percent", Rollout: &half, Value: true}}},
	}}
	if _, _, err := ExperimentRule(cfg, "checkout", "weighted", "legacy"); err != nil {
		t.Fatalf("weighted rule: %v", err)
	}
	for _, tc := range []struct{ flag, rule, control, want string }{
		{"missing", "weighted", "legacy", "does not exist"},
		{"export", "everyone-percent", "false", "not a variant flag"},
		{"checkout", "nope", "legacy", "has no rule"},
		{"checkout", "fixed", "legacy", "serves one value"},
		{"checkout", "by-subject", "legacy", "allocates by subject"},
		{"checkout", "weighted", "other", "is not a variant"},
	} {
		if _, _, err := ExperimentRule(cfg, tc.flag, tc.rule, tc.control); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ExperimentRule(%s, %s, %s) = %v; want %q", tc.flag, tc.rule, tc.control, err, tc.want)
		}
	}
}

func TestExperimentResultsStatuses(t *testing.T) {
	weights := map[string]int{"legacy": 50, "new": 50}
	base := ResultsInput{Control: "legacy", Weights: weights, MixingLift: 0.02, Alpha: 0.05, SRMAlpha: 0.001, MinUnits: 100, MinConversions: 10}
	for _, tc := range []struct {
		name   string
		arms   []Arm
		minP   map[string]float64
		status string
	}{
		{"too few units", []Arm{{Variant: "legacy", Exposed: 50, Converted: 20}, {Variant: "new", Exposed: 50, Converted: 30}}, nil, ExperimentInsufficientData},
		{"only control exposed", []Arm{{Variant: "legacy", Exposed: 500, Converted: 50}}, nil, ExperimentSampleRatioMismatch},
		{"clear win", []Arm{{Variant: "legacy", Exposed: 5000, Converted: 500}, {Variant: "new", Exposed: 5000, Converted: 650}}, nil, ExperimentSignificant},
		{"no difference", []Arm{{Variant: "legacy", Exposed: 5000, Converted: 500}, {Variant: "new", Exposed: 5000, Converted: 505}}, nil, ExperimentNotSignificant},
		{"earlier minimum p-value counts", []Arm{{Variant: "legacy", Exposed: 5000, Converted: 500}, {Variant: "new", Exposed: 5000, Converted: 505}}, map[string]float64{"new": 0.01}, ExperimentSignificant},
		{"allocation mismatch", []Arm{{Variant: "legacy", Exposed: 5000, Converted: 500}, {Variant: "new", Exposed: 6000, Converted: 800}}, nil, ExperimentSampleRatioMismatch},
	} {
		in := base
		in.Arms, in.MinPValues = tc.arms, tc.minP
		got := ExperimentResults(in)
		if len(got.Variants) != 1 || got.Variants[0].Status != tc.status {
			t.Errorf("%s: %+v; want status %s", tc.name, got.Variants, tc.status)
		}
	}
}

func TestValidEventName(t *testing.T) {
	for name, want := range map[string]bool{
		"order_completed": true, "signup.v2": true, "a": true,
		"": false, "Order": false, "1st": false, "has space": false, strings.Repeat("a", 65): false,
	} {
		if got := ValidEventName(name); got != want {
			t.Errorf("ValidEventName(%q) = %v; want %v", name, got, want)
		}
	}
}
