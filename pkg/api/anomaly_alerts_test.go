package api

import "testing"

func TestValidateAnomalyAlertRule(t *testing.T) {
	for _, tc := range []struct {
		name       string
		metric     string
		comparison string
		multiplier float64
		appScoped  bool
		wantOK     bool
	}{
		{"error rate 3x usual", "error_rate_pct", AlertComparisonAboveBaseline, 3, true, true},
		{"p95 at the minimum multiplier", "latency_p95_ms", AlertComparisonAboveBaseline, AnomalyAboveMultiplierMin, true, true},
		{"traffic drop to a third", "request_count", AlertComparisonBelowBaseline, 0.33, true, true},
		{"unsupported metric", "account_spend_eur", AlertComparisonAboveBaseline, 3, true, false},
		{"account-scoped rule", "error_rate_pct", AlertComparisonAboveBaseline, 3, false, false},
		{"multiplier too small to mean anything", "error_rate_pct", AlertComparisonAboveBaseline, 1.1, true, false},
		{"multiplier too large", "error_rate_pct", AlertComparisonAboveBaseline, 50, true, false},
		{"below_baseline only for request_count", "error_rate_pct", AlertComparisonBelowBaseline, 0.5, true, false},
		{"below fraction out of range", "request_count", AlertComparisonBelowBaseline, 0.9, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason := ValidateAnomalyAlertRule(tc.metric, tc.comparison, tc.multiplier, tc.appScoped)
			if (reason == "") != tc.wantOK {
				t.Fatalf("reason = %q, want ok=%v", reason, tc.wantOK)
			}
		})
	}
	if !IsAnomalyAlertComparison("above_baseline") || IsAnomalyAlertComparison("gt") {
		t.Fatal("IsAnomalyAlertComparison misclassifies comparisons")
	}
	if AllowedAlertRuleComparison("above_baseline") {
		t.Fatal("baseline comparisons must stay out of the absolute-threshold set")
	}
}
