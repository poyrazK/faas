package flags

import (
	"math"
	"strings"
	"testing"
)

type xorshift uint64

func (r *xorshift) float() float64 {
	*r ^= *r << 13
	*r ^= *r >> 7
	*r ^= *r << 17
	return float64(uint64(*r)>>11) / (1 << 53)
}

// addUnit adds one customer with requests requests, each an outcome with
// probability p.
func addUnit(a *RatioArm, rng *xorshift, requests int, p float64) {
	var num float64
	for i := 0; i < requests; i++ {
		if rng.float() < p {
			num++
		}
	}
	den := float64(requests)
	a.Units++
	a.Num += num
	a.Den += den
	a.NumSq += num * num
	a.DenSq += den * den
	a.NumDen += num * den
}

func TestEvaluateGuardrailBreachAndTolerance(t *testing.T) {
	rng := xorshift(0x2545f4914f6cdd1d)
	build := func(pControl, pVariant float64, units int) []RatioArm {
		c, v := RatioArm{Variant: "control"}, RatioArm{Variant: "new"}
		for i := 0; i < units; i++ {
			addUnit(&c, &rng, 5+i%20, pControl)
			addUnit(&v, &rng, 5+i%20, pVariant)
		}
		return []RatioArm{c, v}
	}
	in := GuardrailInput{Name: GuardrailServerErrorRate, Tolerance: 0.005, Control: "control", Alpha: 0.01, MinUnits: 100, MinRequests: 1000}
	for _, tc := range []struct {
		name               string
		pControl, pVariant float64
		units              int
		want               string
	}{
		{"clearly worse", 0.01, 0.05, 2000, GuardrailBreached},
		{"same rate", 0.01, 0.01, 2000, GuardrailOK},
		{"worse but within tolerance", 0.01, 0.012, 4000, GuardrailOK},
		{"better", 0.05, 0.01, 2000, GuardrailOK},
		{"too few customers", 0.01, 0.5, 50, GuardrailInsufficientData},
	} {
		in.Arms = build(tc.pControl, tc.pVariant, tc.units)
		got := EvaluateGuardrail(in)
		if got.Status != tc.want || len(got.Variants) != 1 || got.Variants[0].Status != tc.want {
			t.Errorf("%s: %+v; want %s", tc.name, got, tc.want)
		}
	}
}

// Requests of one customer move together; the delta method must not treat
// them as independent. Customers here fail all or none of their requests,
// so a request-level variance would be far too small.
func TestCompareRatioClusteredVariance(t *testing.T) {
	c, v := RatioArm{Variant: "control"}, RatioArm{Variant: "new"}
	add := func(a *RatioArm, requests, fails float64) {
		a.Units++
		a.Num += fails
		a.Den += requests
		a.NumSq += fails * fails
		a.DenSq += requests * requests
		a.NumDen += fails * requests
	}
	for i := 0; i < 200; i++ {
		fails := 0.0
		if i%10 == 0 {
			fails = 50
		}
		add(&c, 50, fails)
		if i%10 == 1 || i%10 == 2 {
			add(&v, 50, 50)
		} else {
			add(&v, 50, 0)
		}
	}
	got, err := CompareRatio(c, v, 0.005, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	naive := math.Sqrt(c.Ratio()*(1-c.Ratio())/c.Den + v.Ratio()*(1-v.Ratio())/v.Den)
	if width := got.High - got.Low; width < 7*naive {
		t.Errorf("interval width %v; want it to reflect customer clustering (request-level sd %v)", width, naive)
	}
}

func TestGuardrailAAFalseBreachRate(t *testing.T) {
	rng := xorshift(0x9e3779b97f4a7c15)
	const runs, batches, perBatch = 200, 20, 100
	breaches := 0
	for r := 0; r < runs; r++ {
		in := GuardrailInput{Name: GuardrailServerErrorRate, Tolerance: 0.005, Control: "a", Alpha: 0.01, MinUnits: 100, MinRequests: 1000}
		a, b := RatioArm{Variant: "a"}, RatioArm{Variant: "b"}
		for i := 0; i < batches; i++ {
			for j := 0; j < perBatch; j++ {
				addUnit(&a, &rng, 10, 0.02)
				addUnit(&b, &rng, 10, 0.02)
			}
			in.Arms = []RatioArm{a, b}
			if EvaluateGuardrail(in).Status == GuardrailBreached {
				breaches++
				break
			}
		}
	}
	if breaches > 0 {
		t.Errorf("A/A guardrail breached %d of %d runs with peeking; want none", breaches, runs)
	}
}

func TestCompareMeans(t *testing.T) {
	rng := xorshift(0x1234567)
	arm := func(name string, mean float64, n int) Arm {
		a := Arm{Variant: name}
		for i := 0; i < n; i++ {
			v := 0.0
			if rng.float() < 0.3 {
				v = mean / 0.3 * (0.5 + rng.float())
			}
			a.Exposed++
			if v != 0 {
				a.Converted++
			}
			a.Sum += v
			a.SumSq += v * v
		}
		return a
	}
	c := arm("control", 1000, 5000)
	if got, err := CompareMeans(c, arm("new", 1300, 5000), 0.1, 0.05); err != nil || got.PValue >= 0.05 || got.Lift <= 0 {
		t.Errorf("30%% higher mean: %+v, %v; want significant positive lift", got, err)
	}
	if got, err := CompareMeans(c, arm("same", 1000, 5000), 0.1, 0.05); err != nil || got.PValue < 0.05 {
		t.Errorf("same mean: %+v, %v; want not significant", got, err)
	}
	if _, err := CompareMeans(Arm{Variant: "a", Exposed: 1}, c, 0.1, 0.05); err == nil {
		t.Error("one unit: want errNoVariance")
	}
}

func TestExperimentResultsMean(t *testing.T) {
	arm := func(name string, n int64, mean, sd float64) Arm {
		// Sum and SumSq of n values with this mean and sample sd.
		return Arm{Variant: name, Exposed: n, Converted: n / 2, Sum: mean * float64(n), SumSq: sd*sd*float64(n-1) + mean*mean*float64(n)}
	}
	got := ExperimentResults(ResultsInput{Kind: ExperimentMetricMean, Control: "a", Weights: map[string]int{"a": 50, "b": 50},
		MixingLift: 0.1, Alpha: 0.05, SRMAlpha: 0.001, MinUnits: 100, MinConversions: 10,
		Arms: []Arm{arm("a", 4000, 20, 30), arm("b", 4000, 25, 30)}})
	if got.ControlRate != 20 || len(got.Variants) != 1 || got.Variants[0].Rate != 25 || got.Variants[0].Status != ExperimentSignificant {
		t.Errorf("mean results = %+v", got)
	}
}

func TestHistogramQuantile(t *testing.T) {
	h := map[int]int64{}
	for ms := int64(1); ms <= 1000; ms++ {
		h[LatencyBucket(ms)]++
	}
	p95 := HistogramQuantile(h, 0.95)
	if p95 < 950 || p95 > 950*1.06 {
		t.Errorf("p95 of 1..1000 ms = %d; want within one 5%% bucket of 950", p95)
	}
	if HistogramQuantile(map[int]int64{}, 0.95) != 0 {
		t.Error("empty histogram: want 0")
	}
	for _, ms := range []int64{1, 9, 100, 86_400_000} {
		if b := LatencyBucket(ms); LatencyBucketUpper(b) < ms || LatencyBucketUpper(b-1) >= ms {
			t.Errorf("bucket %d for %d ms spans (%d, %d]", b, ms, LatencyBucketUpper(b-1), LatencyBucketUpper(b))
		}
	}
}

func TestRecommend(t *testing.T) {
	sig := func(variant string, lift float64) VariantResult {
		return VariantResult{Arm: Arm{Variant: variant}, Status: ExperimentSignificant, Comparison: &Comparison{Variant: variant, Lift: lift, RelativeLift: lift * 10}}
	}
	notSig := VariantResult{Arm: Arm{Variant: "c"}, Status: ExperimentNotSignificant, Comparison: &Comparison{Variant: "c"}}
	ctl := Arm{Variant: "control"}
	breachedB := []GuardrailResult{{Name: GuardrailP95Latency, Variants: []GuardrailVariant{{Variant: "b", Status: GuardrailBreached}}}}
	for _, tc := range []struct {
		name   string
		in     RecommendInput
		action string
		ship   string
	}{
		{"guardrail stop", RecommendInput{StopReason: StopGuardrailBreach, Results: Results{Control: ctl, Variants: []VariantResult{sig("b", 0.1)}}}, RecommendRolledBack, ""},
		{"srm", RecommendInput{Running: true, Results: Results{Control: ctl, SampleRatioError: true, Variants: []VariantResult{sig("b", 0.1)}}}, RecommendFixAllocation, ""},
		{"best of two winners", RecommendInput{Running: true, Results: Results{Control: ctl, Variants: []VariantResult{sig("b", 0.02), sig("c", 0.05)}}}, RecommendShipVariant, "c"},
		{"winner breaching a guardrail", RecommendInput{Running: true, Guardrails: breachedB, Results: Results{Control: ctl, Variants: []VariantResult{sig("b", 0.02)}}}, RecommendKeepRunning, ""},
		{"all worse", RecommendInput{Running: true, Results: Results{Control: ctl, Variants: []VariantResult{sig("b", -0.02)}}}, RecommendKeepControl, ""},
		{"nothing yet", RecommendInput{Running: true, Results: Results{Control: ctl, Variants: []VariantResult{notSig}}}, RecommendKeepRunning, ""},
		{"stopped flat", RecommendInput{StopReason: StopManual, Results: Results{Control: ctl, Variants: []VariantResult{notSig}}}, RecommendDecide, ""},
	} {
		got := Recommend(tc.in)
		if got.Action != tc.action || got.Variant != tc.ship || got.Summary == "" {
			t.Errorf("%s: %+v; want %s %q", tc.name, got, tc.action, tc.ship)
		}
	}
	got := Recommend(RecommendInput{Kind: ExperimentMetricMean, Running: true, Results: Results{Control: ctl, Variants: []VariantResult{sig("b", 2.5)}}})
	if !strings.Contains(got.Summary, "per customer") {
		t.Errorf("mean summary = %q", got.Summary)
	}
}

func TestServeVariant(t *testing.T) {
	cfg := Config{Flags: []Flag{{Key: "checkout", Enabled: true, Default: "legacy",
		Variants: []FlagVariant{{Key: "legacy", Weight: 50}, {Key: "new", Weight: 50}},
		Rules:    []Rule{{ID: "weighted"}, {ID: "fixed", Value: "new"}}}}}
	got, ok := ServeVariant(cfg, "checkout", "weighted", "legacy")
	if !ok || got.Flags[0].Rules[0].Value != "legacy" {
		t.Fatalf("ServeVariant = %+v, %v", got.Flags[0].Rules, ok)
	}
	if cfg.Flags[0].Rules[0].Value != nil {
		t.Error("ServeVariant changed its input")
	}
	for _, tc := range []struct{ flag, rule, variant string }{
		{"missing", "weighted", "legacy"},
		{"checkout", "gone", "legacy"},
		{"checkout", "fixed", "legacy"},
		{"checkout", "weighted", "unknown"},
	} {
		if _, ok := ServeVariant(cfg, tc.flag, tc.rule, tc.variant); ok {
			t.Errorf("ServeVariant(%s, %s, %s) = true; want false", tc.flag, tc.rule, tc.variant)
		}
	}
}
