// adr: 201 — custom application metrics.
package api

import (
	"math"
	"strings"
	"testing"
)

func TestValidateCustomMetricValue(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want string // "" = accept
	}{
		{"zero is a legitimate empty backlog", 0, ""},
		{"ordinary backlog", 1284, ""},
		{"fractional", 0.5, ""},
		{"at the ceiling", CustomMetricMaxValue, ""},
		{"negative", -1, "must be >= 0"},
		{"above the ceiling", CustomMetricMaxValue * 2, "must be <="},
		// NaN must be caught BEFORE the range comparison: every
		// comparison against NaN is false, so a range check alone would
		// pass it straight into the scheduler, where it becomes the
		// numerator of a capacity calculation.
		{"NaN", math.NaN(), "finite"},
		{"positive infinity", math.Inf(1), "finite"},
		{"negative infinity", math.Inf(-1), "finite"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := ValidateCustomMetricValue(c.in)
			if c.want == "" {
				if p != nil {
					t.Fatalf("rejected a valid value: %s", p.Detail)
				}
				return
			}
			if p == nil {
				t.Fatalf("accepted %v, want rejection mentioning %q", c.in, c.want)
			}
			if !strings.Contains(p.Detail, c.want) {
				t.Errorf("detail = %q, want it to mention %q", p.Detail, c.want)
			}
		})
	}
}

func TestValidateCustomMetricName(t *testing.T) {
	valid := []string{"a", "orders_pending", "q1_backlog", strings.Repeat("a", CustomMetricNameMaxBytes)}
	for _, n := range valid {
		if p := ValidateCustomMetricName(n); p != nil {
			t.Errorf("rejected valid name %q: %s", n, p.Detail)
		}
	}
	invalid := []string{
		"",                    // empty
		"1leading_digit",      // must start with a letter
		"_leading_underscore", //
		"Upper",               // lowercase only
		"has-dash",            // underscore only
		"has space",           //
		strings.Repeat("a", CustomMetricNameMaxBytes+1), // too long
	}
	for _, n := range invalid {
		if p := ValidateCustomMetricName(n); p == nil {
			t.Errorf("accepted invalid name %q", n)
		}
	}
}

// TestScalingTargetName_RequiredForCustomOnly pins both halves. A name is
// required for `custom` because it selects WHICH metric, and rejected
// elsewhere because a name on a platform-measured metric would be stored and
// never read — the accepted-but-inert shape this package keeps removing.
func TestScalingTargetName_RequiredForCustomOnly(t *testing.T) {
	if p := ValidateScalingTargets("targets", []ScalingTarget{
		{Metric: ScalingMetricCustom, Name: "orders_pending", Value: 100},
	}); p != nil {
		t.Fatalf("rejected a valid custom target: %s", p.Detail)
	}
	if p := ValidateScalingTargets("targets", []ScalingTarget{
		{Metric: ScalingMetricCustom, Value: 100},
	}); p == nil {
		t.Error("accepted a custom target with no name")
	}
	if p := ValidateScalingTargets("targets", []ScalingTarget{
		{Metric: ScalingMetricCPU, Name: "orders_pending", Value: 70},
	}); p == nil {
		t.Error("accepted a name on cpu, which the platform measures itself")
	}
}

// TestScalingTargets_CustomUniquenessIsPerName pins that the duplicate check
// keys on metric+name. Two custom targets on DIFFERENT names are legitimate
// and common; two on the SAME name are the dead-config case the check exists
// for.
func TestScalingTargets_CustomUniquenessIsPerName(t *testing.T) {
	if p := ValidateScalingTargets("targets", []ScalingTarget{
		{Metric: ScalingMetricCustom, Name: "orders_pending", Value: 100},
		{Metric: ScalingMetricCustom, Name: "docs_queued", Value: 10},
	}); p != nil {
		t.Fatalf("rejected two custom targets on different names: %s", p.Detail)
	}
	p := ValidateScalingTargets("targets", []ScalingTarget{
		{Metric: ScalingMetricCustom, Name: "orders_pending", Value: 100},
		{Metric: ScalingMetricCustom, Name: "orders_pending", Value: 10},
	})
	if p == nil {
		t.Fatal("accepted the same custom name twice; the looser one would be dead config")
	}
	if !strings.Contains(p.Detail, "orders_pending") {
		t.Errorf("detail = %q, want it to name the duplicated metric", p.Detail)
	}
}

// TestCustomMetricsPlanGate pins that the feature is paid-tier. The push
// buys the same scaling capability the other targets do, and an unbounded
// free-tier write endpoint is an abuse surface.
func TestCustomMetricsPlanGate(t *testing.T) {
	if PlanFree.CustomMetricsAllowed() {
		t.Error("Free allows custom metrics")
	}
	for _, p := range []Plan{PlanHobby, PlanPro, PlanScale} {
		if !p.CustomMetricsAllowed() {
			t.Errorf("%s does not allow custom metrics", p)
		}
	}
}
