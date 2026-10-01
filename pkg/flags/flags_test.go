package flags

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

func TestAllocationVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/allocation.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Seed, Key, Customer string
		Bucket              int
	}
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	for _, v := range rows {
		if got := Bucket(v.Seed, v.Key, v.Customer); got != v.Bucket {
			t.Fatalf("%s: %d != %d", v.Customer, got, v.Bucket)
		}
	}
}
func TestStickyRolloutMonotonic(t *testing.T) {
	previous := map[string]bool{}
	for pct := 0; pct <= 10000; pct += 100 {
		b := Bundle{Version: 1, Config: Config{Flags: []Flag{{Key: "export", Enabled: true, Seed: "seed", Rules: []Rule{{ID: "rollout", Rollout: &pct, Value: true}}}}}}
		for i := 0; i < 1000; i++ {
			id := strconv.Itoa(i)
			d := Evaluate(b, "export", id, false)
			value, ok := d.Value.(bool)
			if !ok {
				t.Fatalf("boolean evaluator returned %T", d.Value)
			}
			if previous[id] && !value {
				t.Fatalf("customer %s left increasing rollout", id)
			}
			previous[id] = value
		}
	}
}

func TestVariantAllocationVectorsAndDecisions(t *testing.T) {
	raw, err := os.ReadFile("testdata/variant_allocation.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Seed, Key, Customer string
		Bucket              int
	}
	if err = json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		if got := VariantBucket(vector.Seed, vector.Key, vector.Customer); got != vector.Bucket {
			t.Fatalf("%s: %d != %d", vector.Customer, got, vector.Bucket)
		}
	}

	rollout := 10000
	bundle := Bundle{Version: 7, Config: Config{Flags: []Flag{{Key: "experiment", Type: "variant", Enabled: true, Default: "control", Seed: "stable", Variants: []FlagVariant{{Key: "control", Weight: 5000}, {Key: "treatment", Weight: 5000}}, Rules: []Rule{{ID: "eligible", Rollout: &rollout}}}}}}
	if err := Validate(bundle.Config); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for i := 0; i < 10000; i++ {
		customer := "customer-" + strconv.Itoa(i)
		decision := EvaluateVariant(bundle, "experiment", customer, "fallback")
		value, ok := decision.Value.(string)
		if !ok || value != "control" && value != "treatment" || decision.Type != "variant" || decision.RuleID != "eligible" || decision.Bucket == nil || *decision.Bucket != VariantBucket("stable", "experiment", customer) {
			t.Fatalf("unexpected variant decision for %s: %+v", customer, decision)
		}
		counts[value]++
	}
	if counts["control"] < 4700 || counts["control"] > 5300 {
		t.Fatalf("weighted allocation is not balanced: %v", counts)
	}

	pct := 1000
	bundle.Flags[0].Rules[0].Rollout = &pct
	eligible, excluded := 0, 0
	for i := 0; i < 10000; i++ {
		customer := "customer-" + strconv.Itoa(i)
		decision := EvaluateVariant(bundle, "experiment", customer, "fallback")
		if decision.Reason == "rule_match" {
			eligible++
			if decision.RolloutBucket == nil || *decision.RolloutBucket >= pct || decision.Bucket == nil {
				t.Fatalf("missing independent rollout/allocation buckets: %+v", decision)
			}
		} else if decision.Value != "control" {
			t.Fatalf("ineligible customer received a non-default variant: %+v", decision)
		} else {
			excluded++
		}
	}
	if eligible < 850 || eligible > 1150 || excluded+eligible != 10000 {
		t.Fatalf("rollout cohort size is unexpected: eligible=%d excluded=%d", eligible, excluded)
	}
}

func TestVariantValidationAndExplicitTargeting(t *testing.T) {
	config := Config{Flags: []Flag{{Key: "checkout", Type: "variant", Enabled: true, Default: "old", Seed: "seed", Variants: []FlagVariant{{Key: "old", Weight: 7000}, {Key: "new", Weight: 3000}}, Rules: []Rule{{ID: "customer", Customers: []string{"customer"}, Value: "new"}}}}}
	if err := Validate(config); err != nil {
		t.Fatal(err)
	}
	d := EvaluateVariant(Bundle{Version: 3, Config: config}, "checkout", "customer", "fallback")
	if d.Value != "new" || d.Reason != "rule_match" || d.RuleID != "customer" || d.Bucket != nil {
		t.Fatal(d)
	}
	config.Flags[0].Variants[1].Weight = 2000
	if err := Validate(config); err == nil {
		t.Fatal("accepted variant weights that do not total 10000")
	}
}

func TestProgressiveRolloutValidation(t *testing.T) {
	rollout := 100
	config := Config{Flags: []Flag{{
		Key: "new-export", Enabled: true,
		Rules: []Rule{{ID: "customers", Rollout: &rollout, Value: true, Progression: &ProgressiveRollout{
			Stages: []int{100, 1000, 10000}, CurrentStage: 0,
			MinimumUsedRequests: 50, MaximumHTTP5xxRateBasisPoints: 200,
			MaximumP95LatencyMS: 500, WindowSeconds: 900,
		}}},
	}}}
	if err := Validate(config); err != nil {
		t.Fatalf("valid progression: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "rollout does not match current stage", mutate: func(c *Config) { c.Flags[0].Rules[0].Rollout = intPtr(500) }},
		{name: "stages are not increasing", mutate: func(c *Config) { c.Flags[0].Rules[0].Progression.Stages = []int{100, 100, 10000} }},
		{name: "stages do not reach full rollout", mutate: func(c *Config) {
			c.Flags[0].Rules[0].Progression.Stages[2] = 9000
			c.Flags[0].Rules[0].Rollout = intPtr(100)
		}},
		{name: "true behavior required", mutate: func(c *Config) { c.Flags[0].Rules[0].Value = false }},
		{name: "sample threshold required", mutate: func(c *Config) { c.Flags[0].Rules[0].Progression.MinimumUsedRequests = 0 }},
		{name: "window bounded", mutate: func(c *Config) { c.Flags[0].Rules[0].Progression.WindowSeconds = 30 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneConfig(config)
			tt.mutate(&candidate)
			if err := Validate(candidate); err == nil {
				t.Fatal("invalid progressive rollout accepted")
			}
		})
	}
}

func intPtr(value int) *int { return &value }

func cloneConfig(c Config) Config {
	raw, _ := json.Marshal(c)
	var clone Config
	_ = json.Unmarshal(raw, &clone)
	return clone
}

func TestTargetingOrderAndAnonymous(t *testing.T) {
	pct := 10000
	b := Bundle{Version: 12, Config: Config{Groups: map[string][]string{"internal": {"a"}}, Flags: []Flag{{Key: "export", Seed: "seed", Enabled: true, Rules: []Rule{
		{ID: "excluded", Customers: []string{"a"}, Value: false}, {ID: "internal", Group: "internal", Value: true}, {ID: "eligible", Customers: []string{"b"}, Rollout: &pct, Value: true},
	}}}}}
	for _, tc := range []struct {
		id, reason, rule string
		value            bool
	}{{"a", "rule_match", "excluded", false}, {"b", "rule_match", "eligible", true}, {"c", "default", "", false}, {"", "customer_missing", "", false}} {
		d := Evaluate(b, "export", tc.id, true)
		if d.Value != tc.value || d.Reason != tc.reason || d.RuleID != tc.rule || d.ConfigVersion != 12 {
			t.Fatalf("%s: %+v", tc.id, d)
		}
	}
	b.Flags[0].Enabled = false
	if d := Evaluate(b, "export", "a", true); d.Reason != "disabled" || d.Value != false {
		t.Fatal(d)
	}
	if d := Evaluate(b, "missing", "a", true); d.Value != true || d.Source != "fallback" {
		t.Fatal(d)
	}
}

func TestLegacyBooleanFalseValuesKeepTheirWireShape(t *testing.T) {
	config := Config{Flags: []Flag{{Key: "export", Enabled: true, Default: false, Rules: []Rule{{ID: "off", Customers: []string{"customer"}, Value: false}}}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	flag := decoded["flags"].([]any)[0].(map[string]any)
	if flag["default"] != false || flag["rules"].([]any)[0].(map[string]any)["value"] != false {
		t.Fatalf("legacy false values were omitted: %s", raw)
	}
}
func TestEvidenceRejectsMalformedAndCanonicalizes(t *testing.T) {
	for _, raw := range []string{`[{"flag":"export","config_version":-1}]`, `[{"flag":"export","reason":"arbitrary","source":"configuration"}]`, `[] {}`, `[{"flag":"export","reason":"default","source":"configuration","secret":"x"}]`} {
		if _, err := CanonicalEvidence([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	raw := `[{"flag":"z","value":true,"config_version":1,"reason":"default","source":"configuration","used":true},{"flag":"a","value":false,"config_version":1,"reason":"disabled","source":"configuration","used":false}]`
	out, err := CanonicalEvidence([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var rows []Evidence
	_ = json.Unmarshal([]byte(out), &rows)
	if rows[0].Flag != "a" {
		t.Fatal(out)
	}
}

func TestVariantEvidenceCanonicalizesAndValidatesTypedValue(t *testing.T) {
	raw := `[{"flag":"checkout","type":"variant","value":"new","config_version":2,"rule_id":"rollout","reason":"rule_match","bucket":8123,"rollout_bucket":543,"source":"configuration","used":true}]`
	out, err := CanonicalEvidence([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var rows []Evidence
	if err = json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0].Value != "new" || rows[0].Type != "variant" || rows[0].RolloutBucket == nil || *rows[0].RolloutBucket != 543 {
		t.Fatalf("canonical variant evidence = %s (%v)", out, err)
	}
	for _, invalid := range []string{
		`[{"flag":"checkout","type":"variant","value":true,"config_version":2,"reason":"default","source":"configuration","used":false}]`,
		`[{"flag":"checkout","value":"new","config_version":2,"reason":"default","source":"configuration","used":false}]`,
		`[{"flag":"checkout","type":"variant","value":"new","config_version":2,"reason":"type_mismatch","source":"configuration","used":false}]`,
	} {
		if _, err := CanonicalEvidence([]byte(invalid)); err == nil {
			t.Fatalf("accepted malformed variant evidence: %s", invalid)
		}
	}
}
