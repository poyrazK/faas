package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMCPAccessFingerprint(t *testing.T) {
	now := time.Now()
	resource := "https://reports.example/mcp"
	rule := EdgeRuleResponse{ID: "one", AccountID: "account", AppID: "app", Kind: "jwt", MatchHost: "reports.example", MatchPath: "/**", Enabled: true, Action: json.RawMessage(`{"issuer":"https://issuer.example","required_claims":{"secret":"hidden"}}`)}
	original, err := MCPAccessPolicyFingerprint(resource, []EdgeRuleResponse{rule}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"action", "priority", "disabled", "expiry", "wildcard", "headers", "methods", "path", "condition", "metadata", "unrelated", "ordering", "json ordering"} {
		t.Run(name, func(t *testing.T) {
			changed := rule
			rules := []EdgeRuleResponse{changed}
			wantDifferent := true
			switch name {
			case "action":
				rules[0].Action = json.RawMessage(`{"issuer":"https://changed.example"}`)
			case "priority":
				rules[0].Priority++
			case "disabled":
				rules[0].Enabled = false
			case "expiry":
				future := now.Add(time.Minute)
				rules[0].ExpiresAt = &future
			case "wildcard":
				other := rule
				other.ID = "two"
				other.AppID = "other"
				other.MatchHost = "*.example"
				rules = append(rules, other)
			case "headers":
				rules[0].MatchHeaders = map[string]string{"X-Private": "hidden"}
			case "methods":
				rules[0].MatchMethods = []string{"GET"}
			case "path":
				rules[0].MatchPath = "/mcp"
			case "condition":
				rules[0].Mode = "log"
			case "metadata":
				rules[0].Name = "renamed"
				rules[0].UpdatedAt = now
				wantDifferent = false
			case "unrelated":
				other := rule
				other.ID = "two"
				other.MatchHost = "other.example"
				rules = append(rules, other)
				wantDifferent = false
			case "ordering":
				rules[0].MatchMethods = []string{"GET", "POST"}
				other := rules[0]
				other.MatchMethods = []string{"POST", "GET", "POST"}
				a, _ := MCPAccessPolicyFingerprint(resource, rules, now)
				b, _ := MCPAccessPolicyFingerprint(resource, []EdgeRuleResponse{other}, now)
				if a != b {
					t.Fatal("method order changed fingerprint")
				}
				return
			case "json ordering":
				rules[0].Action = json.RawMessage(`{ "required_claims": {"secret":"hidden"}, "issuer":"https://issuer.example" }`)
				wantDifferent = false
			}
			fingerprint, err := MCPAccessPolicyFingerprint(resource, rules, now)
			if err != nil {
				t.Fatal(err)
			}
			if (fingerprint != original) != wantDifferent {
				t.Fatalf("fingerprint changed=%t, want %t", fingerprint != original, wantDifferent)
			}
			if err := (MCPAccessPolicyExpectation{Resource: resource, Fingerprint: fingerprint}).Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	expires := now.Add(time.Second)
	rule.ExpiresAt = &expires
	before, _ := MCPAccessPolicyFingerprint(resource, []EdgeRuleResponse{rule}, now)
	after, _ := MCPAccessPolicyFingerprint(resource, []EdgeRuleResponse{rule}, now.Add(time.Second))
	if before == after {
		t.Fatal("expiry did not invalidate observation")
	}
}
