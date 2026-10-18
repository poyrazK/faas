package api

import "testing"

// adr: 1191 — "@app" is the only accepted "@"-prefixed match_host, and as a
// pattern it covers any host (callers scope it to the rule's app).
func TestEdgeRuleAppHosts(t *testing.T) {
	for host, ok := range map[string]bool{
		"@app":            true,
		"@APP":            true,
		"api.example.com": true,
		"*.example.com":   true,
		"@all":            false,
		"@":               false,
		"":                false,
	} {
		if got := ValidateEdgeRuleMatchHost(host) == nil; got != ok {
			t.Errorf("ValidateEdgeRuleMatchHost(%q) ok=%v, want %v", host, got, ok)
		}
	}
	if !EdgeRuleHostMatches("@app", "shop.example.com") || !IsEdgeRuleAppHosts(" @App ") {
		t.Fatal("@app must cover any host")
	}
	if EdgeRuleHostMatches("@app", "") || EdgeRuleHostMatches("api.example.com", "shop.example.com") {
		t.Fatal("unexpected host match")
	}
}
