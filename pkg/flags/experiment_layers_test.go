package flags

import (
	"math"
	"strings"
	"testing"
)

func layeredConfig(x RuleExperiment) Config {
	return Config{Groups: map[string][]string{}, Layers: []Layer{{Key: "l1", Seed: "s"}}, Holdout: &Holdout{Seed: "h", BasisPoints: 500},
		Flags: []Flag{{Key: "f", Type: "variant", Enabled: true, Default: "a", Seed: "s",
			Variants: []FlagVariant{{Key: "a", Weight: 5000}, {Key: "b", Weight: 5000}},
			Rules:    []Rule{{ID: "r", Rollout: ptr(10000), Experiment: &x}}}}}
}

func ptr(v int) *int { return &v }

// ADR 20261119173702 §2: a rule experiment must name a variant control, a
// matching unit and an existing layer, and layered experiments never share
// buckets.
func TestValidateRuleExperiments(t *testing.T) {
	ok := RuleExperiment{Key: "x1", Control: "a", Unit: ExperimentUnitCustomer, Layer: "l1", From: 0, To: 3000}
	if err := Validate(layeredConfig(ok)); err != nil {
		t.Fatalf("valid layered experiment rejected: %v", err)
	}
	for _, tc := range []struct {
		name string
		x    RuleExperiment
		want string
	}{
		{"control", RuleExperiment{Key: "x1", Control: "zzz", Unit: ExperimentUnitCustomer}, "control"},
		{"unit", RuleExperiment{Key: "x1", Control: "a", Unit: ExperimentUnitSubject}, "rollout_unit subject"},
		{"layer", RuleExperiment{Key: "x1", Control: "a", Unit: ExperimentUnitCustomer, Layer: "nope", To: 10}, "layer"},
		{"slice", RuleExperiment{Key: "x1", Control: "a", Unit: ExperimentUnitCustomer, Layer: "l1", From: 5000, To: 5000}, "slice"},
		{"slice without layer", RuleExperiment{Key: "x1", Control: "a", Unit: ExperimentUnitCustomer, To: 10}, "layer"},
	} {
		if err := Validate(layeredConfig(tc.x)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v; want an error naming %q", tc.name, err, tc.want)
		}
	}
	overlap := layeredConfig(ok)
	second := RuleExperiment{Key: "x2", Control: "a", Unit: ExperimentUnitCustomer, Layer: "l1", From: 2000, To: 4000}
	overlap.Flags = append(overlap.Flags, Flag{Key: "g", Type: "variant", Enabled: true, Default: "a", Seed: "s",
		Variants: []FlagVariant{{Key: "a", Weight: 5000}, {Key: "b", Weight: 5000}},
		Rules:    []Rule{{ID: "r", Rollout: ptr(10000), Experiment: &second}}})
	if err := Validate(overlap); err == nil || !strings.Contains(err.Error(), "share buckets") {
		t.Fatalf("overlapping slices = %v", err)
	}
	big := layeredConfig(ok)
	big.Holdout.BasisPoints = 2001
	if err := Validate(big); err == nil {
		t.Fatal("a 20.01% holdout was accepted")
	}
}

// ADR 20261119173702 §2: about the holdout's share of customers is held
// out, and units outside a layer slice fall through to the next rule.
func TestHoldoutAndLayerShares(t *testing.T) {
	cfg := layeredConfig(RuleExperiment{Key: "x1", Control: "a", Unit: ExperimentUnitCustomer, Layer: "l1", From: 0, To: 3000})
	b := Bundle{Version: 1, Config: cfg}
	counts := map[string]int{}
	const n = 20000
	for i := 0; i < n; i++ {
		d := EvaluateVariantWithContext(b, "f", EvaluationContext{Customer: "c-" + string(rune('a'+i%26)) + itoa(i)}, "a")
		counts[d.Reason]++
		if d.Reason == ReasonHoldout && (d.Value != "a" || d.RuleID != "r") {
			t.Fatalf("holdout decision = %+v", d)
		}
	}
	// 30% are in the slice; 5% of those are held out.
	if got := float64(counts[ReasonHoldout]) / n; math.Abs(got-0.015) > 0.005 {
		t.Errorf("holdout share = %.4f; want about 0.015", got)
	}
	if got := float64(counts["rule_match"]) / n; math.Abs(got-0.285) > 0.02 {
		t.Errorf("rule_match share = %.4f; want about 0.285", got)
	}
	if got := float64(counts["default"]) / n; math.Abs(got-0.70) > 0.02 {
		t.Errorf("default share = %.4f; want about 0.70", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func TestLayerSlice(t *testing.T) {
	for _, tc := range []struct {
		taken     [][2]int
		traffic   int
		from, to  int
		available bool
	}{
		{nil, 3000, 0, 3000, true},
		{[][2]int{{0, 3000}}, 3000, 3000, 6000, true},
		{[][2]int{{2000, 4000}}, 2000, 0, 2000, true},
		{[][2]int{{2000, 4000}}, 2500, 4000, 6500, true},
		{[][2]int{{0, 6000}, {6000, 10000}}, 1, 0, 0, false},
		{nil, 0, 0, 0, false},
	} {
		from, to, ok := LayerSlice(tc.taken, tc.traffic)
		if from != tc.from || to != tc.to || ok != tc.available {
			t.Errorf("LayerSlice(%v, %d) = %d, %d, %v", tc.taken, tc.traffic, from, to, ok)
		}
	}
}

// ADR 20261119173702 §4: a clearly better arm gets most traffic, every arm
// keeps the floor, and weights sum to 10000.
func TestBanditPosteriorAndWeights(t *testing.T) {
	arms := []Arm{{Variant: "a", Exposed: 1000, Converted: 100}, {Variant: "b", Exposed: 1000, Converted: 160}, {Variant: "c", Exposed: 1000, Converted: 90}}
	post := BanditPosterior(arms, 10_000, BanditRNG("exp", 1))
	if post[1].Variant != "b" || post[1].BestProbability < 0.99 || post[1].ExpectedLoss > 0.001 || post[0].ExpectedLoss < 0.05 {
		t.Fatalf("posterior = %+v", post)
	}
	w := BanditWeights(post, 1000)
	if w["a"]+w["b"]+w["c"] != 10000 || w["a"] < 1000 || w["c"] < 1000 || w["b"] < 7500 {
		t.Fatalf("weights = %v", w)
	}
	again := BanditPosterior(arms, 10_000, BanditRNG("exp", 1))
	if again[0] != post[0] {
		t.Fatal("the same experiment and pass must draw the same posterior")
	}
	even := BanditWeights(BanditPosterior([]Arm{{Variant: "a"}, {Variant: "b"}}, 10_000, BanditRNG("e", 2)), 1000)
	if math.Abs(float64(even["a"]-5000)) > 300 {
		t.Fatalf("no data weights = %v; want about even", even)
	}
	if BanditShift(map[string]int{"a": 5000, "b": 5000}, map[string]int{"a": 4400, "b": 5600}) != 600 {
		t.Fatal("BanditShift")
	}
}

func TestSetVariantWeights(t *testing.T) {
	cfg := layeredConfig(RuleExperiment{Key: "x1", Control: "a", Unit: ExperimentUnitCustomer})
	out, ok := SetVariantWeights(cfg, "f", map[string]int{"a": 3000, "b": 7000})
	if !ok || out.Flags[0].Variants[1].Weight != 7000 || cfg.Flags[0].Variants[1].Weight != 5000 {
		t.Fatalf("SetVariantWeights = %+v %v; the input must not change", out.Flags[0].Variants, ok)
	}
	if _, ok := SetVariantWeights(cfg, "f", map[string]int{"a": 3000, "b": 6000}); ok {
		t.Fatal("weights not summing to 10000 were accepted")
	}
}

func TestBenjaminiHochberg(t *testing.T) {
	q := BenjaminiHochberg([]float64{0.01, 0.04, 0.03, 0.5})
	want := []float64{0.04, 0.16 / 3, 0.16 / 3, 0.5}
	for i := range q {
		if math.Abs(q[i]-want[i]) > 1e-9 {
			t.Fatalf("q = %v; want %v", q, want)
		}
	}
}

func TestComparePercentile(t *testing.T) {
	control := make([]float64, 400)
	treated := make([]float64, 400)
	for i := range control {
		control[i] = float64(i)
		treated[i] = float64(i) + 100
	}
	c, err := ComparePercentile("b", control, treated, 0.9, 500, BanditRNG("p", 1))
	if err != nil || math.Abs(c.Lift-100) > 1e-9 || c.Low <= 0 || c.PValue > 0.01 {
		t.Fatalf("percentile = %+v, %v", c, err)
	}
	same, _ := ComparePercentile("b", control, control, 0.5, 500, BanditRNG("p", 2))
	if same.PValue < 0.5 || same.Low > 0 || same.High < 0 {
		t.Fatalf("equal arms = %+v", same)
	}
}

// ADR 20261119173702 §5: a clearly worse stage rolls back, a fine stage
// with enough units advances, and a small one holds.
func TestRolloutMetricDecision(t *testing.T) {
	m := RolloutMetric{Event: "order"}
	worse, _ := RolloutMetricDecision(m, Arm{Exposed: 5000, Converted: 1000}, Arm{Exposed: 5000, Converted: 600}, 0.02, 0.05)
	fine, _ := RolloutMetricDecision(m, Arm{Exposed: 500, Converted: 100}, Arm{Exposed: 500, Converted: 102}, 0.02, 0.05)
	small, _ := RolloutMetricDecision(m, Arm{Exposed: 50, Converted: 10}, Arm{Exposed: 50, Converted: 10}, 0.02, 0.05)
	if worse != RolloutRollback || fine != RolloutAdvance || small != RolloutHold {
		t.Fatalf("decisions = %s %s %s", worse, fine, small)
	}
	bad := layeredConfig(RuleExperiment{Key: "x1", Control: "a", Unit: ExperimentUnitCustomer})
	bad.Flags = append(bad.Flags, Flag{Key: "roll", Enabled: true, Default: false, Seed: "s", Rules: []Rule{{ID: "r", Rollout: ptr(100), Value: true,
		Progression: &ProgressiveRollout{Stages: []int{100, 10000}, MinimumUsedRequests: 1, MaximumP95LatencyMS: 100, WindowSeconds: 3600,
			Metric: &RolloutMetric{Event: "Bad Name"}}}}})
	if err := Validate(bad); err == nil || !strings.Contains(err.Error(), "rollout metric") {
		t.Fatalf("invalid rollout metric = %v", err)
	}
}

// ADR 20261119173702 §2: holdout evidence names its rule.
func TestHoldoutEvidenceIsValid(t *testing.T) {
	if err := validateDecision(Decision{Flag: "f", Type: "variant", Value: "a", ConfigVersion: 1, RuleID: "r", Reason: ReasonHoldout, Source: "configuration"}, false); err != nil {
		t.Fatalf("holdout evidence = %v", err)
	}
	if err := validateDecision(Decision{Flag: "f", Type: "variant", Value: "a", ConfigVersion: 1, Reason: ReasonHoldout, Source: "configuration"}, false); err == nil {
		t.Fatal("holdout evidence without a rule was accepted")
	}
}
