package api

import (
	"net/http"
	"testing"
)

// adr: 20261022083601 — amendment 1: with the edge_ai_crawlers flag off for
// the account, the AI crawler fields read as absent, so a condition on them
// never matches, and a negated one always does.
func TestEdgeRuleMatchAICrawlerFieldsUnreleased(t *testing.T) {
	headers := http.Header{}
	headers.Set("User-Agent", "Mozilla/5.0 (compatible; GPTBot/1.1)")
	verified := func() string { return "gptbot" }
	for name, expr := range map[string]EdgeRuleMatchExpr{
		"ai_crawler":          {Field: "ai_crawler", Op: "eq", Value: "gptbot"},
		"ai_crawler_purpose":  {Field: "ai_crawler_purpose", Op: "eq", Value: "training"},
		"verified_ai_crawler": {Field: "verified_ai_crawler", Op: "eq", Value: "gptbot"},
	} {
		prog, err := CompileEdgeRuleMatch(&expr)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		released := EdgeRuleMatchInput{Headers: headers, VerifiedAICrawler: verified}
		if !prog.Matches(released) {
			t.Fatalf("%s: released account did not match GPTBot", name)
		}
		unreleased := released
		unreleased.AICrawlersUnreleased = true
		if prog.Matches(unreleased) {
			t.Errorf("%s: matched for an account the flag leaves out", name)
		}
	}
}

// adr: 20261022083601 — amendment 1: the release gate finds an AI crawler
// field anywhere in a condition.
func TestEdgeRuleMatchUsesAICrawlerFields(t *testing.T) {
	for name, tc := range map[string]struct {
		expr *EdgeRuleMatchExpr
		want bool
	}{
		"none":          {nil, false},
		"other field":   {&EdgeRuleMatchExpr{Field: "country", Op: "eq", Value: "US"}, false},
		"top level":     {&EdgeRuleMatchExpr{Field: "ai_crawler", Op: "exists"}, true},
		"inside all":    {&EdgeRuleMatchExpr{All: []EdgeRuleMatchExpr{{Field: "country", Op: "eq", Value: "US"}, {Field: "ai_crawler_purpose", Op: "eq", Value: "training"}}}, true},
		"inside any":    {&EdgeRuleMatchExpr{Any: []EdgeRuleMatchExpr{{Field: "path", Op: "eq", Value: "/"}}}, false},
		"inside not":    {&EdgeRuleMatchExpr{Not: &EdgeRuleMatchExpr{Field: "verified_ai_crawler", Op: "exists"}}, true},
		"deeply nested": {&EdgeRuleMatchExpr{Any: []EdgeRuleMatchExpr{{All: []EdgeRuleMatchExpr{{Not: &EdgeRuleMatchExpr{Field: "ai_crawler", Op: "exists"}}}}}}, true},
	} {
		if got := EdgeRuleMatchUsesAICrawlerFields(tc.expr); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}
