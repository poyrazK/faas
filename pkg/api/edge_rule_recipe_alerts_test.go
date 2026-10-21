package api

import "testing"

// adr: 986 — a recipe's spike alert is an edge_rule_activity alert scoped to
// the recipe's rule, hourly, webhook-only, with a unique short name.
func TestEdgeRuleRecipeAlert(t *testing.T) {
	req, prob := EdgeRuleRecipeAlert(EdgeRuleRecipeBlockUnverifiedBots, "0123456789abcdef", 250, " https://hooks.example.com/x ", "s3cret")
	if prob != nil {
		t.Fatal(prob.Detail)
	}
	if req.Metric != AlertMetricEdgeRuleActivity || req.EdgeRuleID != "0123456789abcdef" || req.Threshold != 250 ||
		req.WindowSpec != "1h" || req.Comparison != "gt" || req.WebhookURL != "https://hooks.example.com/x" || req.Action != nil {
		t.Fatalf("alert request = %+v", req)
	}
	if req.Name != "Block unverified bots spike (01234567)" || len(req.Name) > AlertRuleNameMaxChars {
		t.Fatalf("name = %q", req.Name)
	}
	for _, r := range EdgeRuleRecipes {
		if got, _ := EdgeRuleRecipeAlert(r.ID, "0123456789abcdef", 1, "https://h", "s"); len(got.Name) > AlertRuleNameMaxChars {
			t.Errorf("%s: alert name %q too long", r.ID, got.Name)
		}
	}
	for name, tc := range map[string]struct {
		recipe, rule, url, secret string
		perHour                   int
	}{
		"unknown recipe": {"nope", "r", "https://h", "s", 1},
		"no rule id":     {EdgeRuleRecipeBlockCountries, "", "https://h", "s", 1},
		"zero threshold": {EdgeRuleRecipeBlockCountries, "r", "https://h", "s", 0},
		"no webhook":     {EdgeRuleRecipeBlockCountries, "r", " ", "s", 1},
		"no secret":      {EdgeRuleRecipeBlockCountries, "r", "https://h", "", 1},
	} {
		if _, prob := EdgeRuleRecipeAlert(tc.recipe, tc.rule, tc.perHour, tc.url, tc.secret); prob == nil {
			t.Errorf("%s: built, want error", name)
		}
	}
}
