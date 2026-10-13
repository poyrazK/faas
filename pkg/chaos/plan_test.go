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

func TestDNSRuleValidationAndConflictBounds(t *testing.T) {
	for _, rule := range []Rule{
		{To: "cache", Kind: KindDNSLatency, Percent: 100, LatencyMS: 250},
		{To: "cache", Kind: KindDNSTimeout, Percent: 100},
		{To: "cache", Kind: KindDNSServFail, Percent: 100},
		{To: "cache", Kind: KindDNSNXDomain, Percent: 100},
	} {
		if err := (Plan{DurationMS: 5_000, Rules: []Rule{rule}}).Validate(); err != nil {
			t.Errorf("valid DNS rule %+v rejected: %v", rule, err)
		}
	}
	for _, invalid := range []Plan{
		{DurationMS: 5_000, Rules: []Rule{{To: "cache", Kind: KindDNSLatency, Percent: 100, LatencyMS: 30_001}}},
		{DurationMS: 5_000, Rules: []Rule{{To: "cache", Kind: KindDNSTimeout, Percent: 100, Port: 53}}},
		{DurationMS: 5_000, Rules: []Rule{{To: "cache", Kind: KindDNSServFail, Percent: 100}, {From: "worker", To: "cache", Kind: KindDNSNXDomain, Percent: 100}}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("invalid DNS plan accepted: %+v", invalid)
		}
	}
}

func TestHTTPRetryAfterValidation(t *testing.T) {
	for _, tc := range []struct {
		status int
		retry  int64
		valid  bool
	}{
		{429, 0, true}, {429, 1, true}, {503, 30, true}, {500, 0, true}, {599, 0, true},
		{428, 0, false}, {430, 0, false}, {499, 0, false}, {600, 0, false},
		{429, -1, false}, {429, 31, false}, {500, 1, false}, {503, 31, false},
	} {
		rule := Rule{To: "inventory", Kind: KindHTTPStatus, Percent: 100, StatusCode: tc.status, RetryAfterSeconds: tc.retry}
		if err := ValidateRule(rule); (err == nil) != tc.valid {
			t.Fatalf("status=%d retry=%d: %v", tc.status, tc.retry, err)
		}
	}
	for _, rule := range []Rule{
		{To: "inventory", Kind: KindLatency, Percent: 100, LatencyMS: 1, RetryAfterSeconds: 1},
		{To: "inventory", Kind: KindTCPLatency, Percent: 100, Port: 6379, LatencyMS: 1, RetryAfterSeconds: 1},
		{To: "inventory", Kind: KindDNSTimeout, Percent: 100, RetryAfterSeconds: 1},
	} {
		if ValidateRule(rule) == nil {
			t.Fatalf("accepted incompatible retry header: %+v", rule)
		}
	}
	original := Rule{To: "inventory", Kind: KindHTTPStatus, Percent: 100, StatusCode: 429}
	changed := original
	changed.RetryAfterSeconds = 1
	if RuleID(original) == RuleID(changed) {
		t.Fatal("retry header omitted from match identity")
	}
}

func TestHTTPBodyFaultValidation(t *testing.T) {
	for _, kind := range []string{KindHTTPMalformedJSON, KindHTTPTruncatedBody, KindHTTPResponseLost} {
		original := Rule{To: "inventory", Kind: kind, Percent: 100}
		if err := ValidateRule(original); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*Rule){
			func(r *Rule) { r.StatusCode = 200 }, func(r *Rule) { r.LatencyMS = 1 },
			func(r *Rule) { r.RetryAfterSeconds = 1 }, func(r *Rule) { r.Port = 6379 },
			func(r *Rule) { r.Direction = DirectionBoth }, func(r *Rule) { r.RateKiBPerSecond = 1 }, func(r *Rule) { r.ResetAfterMS = 1 },
		} {
			rule := original
			mutate(&rule)
			if ValidateRule(rule) == nil {
				t.Fatalf("incompatible body parameters accepted: %+v", rule)
			}
		}
	}
	a := Rule{To: "inventory", Kind: KindHTTPMalformedJSON, Percent: 100}
	b := a
	b.Kind = KindHTTPTruncatedBody
	if RuleID(a) == RuleID(b) {
		t.Fatal("body faults share a match identity")
	}
}
