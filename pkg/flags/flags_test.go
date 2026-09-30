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
			if previous[id] && !d.Value {
				t.Fatalf("customer %s left increasing rollout", id)
			}
			previous[id] = d.Value
		}
	}
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
	if d := Evaluate(b, "export", "a", true); d.Reason != "disabled" || d.Value {
		t.Fatal(d)
	}
	if d := Evaluate(b, "missing", "a", true); !d.Value || d.Source != "fallback" {
		t.Fatal(d)
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
