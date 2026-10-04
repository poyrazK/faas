package chaos

import "testing"

func TestPlanValidationBoundsAndMemberScope(t *testing.T) {
	plan := Plan{DurationMS: 5_000, Rules: []Rule{{
		From: "worker", To: "inventory", Kind: KindHTTPStatus,
		Percent: 10, StatusCode: 503, Seed: 17,
	}}}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	if err := plan.ValidateWorkloads(map[string]struct{}{"api": {}, "worker": {}, "inventory": {}}); err != nil {
		t.Fatalf("valid workload scope rejected: %v", err)
	}
	if err := plan.ValidateWorkloads(map[string]struct{}{"api": {}, "worker": {}}); err == nil {
		t.Fatal("unknown target workload accepted")
	}
	for _, invalid := range []Plan{
		{DurationMS: 999, Rules: plan.Rules},
		{DurationMS: 300_001, Rules: plan.Rules},
		{DurationMS: 5_000, Rules: []Rule{{To: "inventory", Kind: KindLatency, Percent: 100, LatencyMS: 30_001}}},
		{DurationMS: 5_000, Rules: []Rule{{To: "inventory", Kind: KindHTTPStatus, Percent: 101, StatusCode: 503}}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid plan accepted: %+v", invalid)
		}
	}
}

func TestSelectIsStableForTraceIDsAndPercentBoundaries(t *testing.T) {
	rule := Rule{Percent: 20, Seed: 5}
	if !Select(Rule{Percent: 100}, 1, "trace-a") || Select(Rule{Percent: 0}, 1, "trace-a") {
		t.Fatal("100% and 0% boundaries are incorrect")
	}
	first := Select(rule, 1, "trace-a")
	if again := Select(rule, 99, "trace-a"); again != first {
		t.Fatalf("trace decision changed with ordinal: %v vs %v", first, again)
	}
	selected, skipped := 0, 0
	for ordinal := uint64(1); ordinal <= 100; ordinal++ {
		if Select(rule, ordinal, "") {
			selected++
		} else {
			skipped++
		}
	}
	if selected == 0 || skipped == 0 {
		t.Fatalf("20%% selection did not select a fraction of requests: selected=%d skipped=%d", selected, skipped)
	}
}
