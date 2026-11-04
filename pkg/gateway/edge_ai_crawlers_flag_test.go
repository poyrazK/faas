package gateway

import (
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261022083601 — amendment 1: once the handler marks the request's
// match context unreleased, the AI crawler fields read as absent in every
// condition built from it.
func TestEdgeRuleMatchInputAICrawlersUnreleased(t *testing.T) {
	r := httptest.NewRequest("GET", "http://shop.example.test/", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 (compatible; GPTBot/1.1)")
	m := NewEdgeRuleMatchContext(r, nil, nil, nil)
	ctx := WithEdgeRuleMatchContext(r.Context(), m)
	prog, err := api.CompileEdgeRuleMatch(&api.EdgeRuleMatchExpr{Field: "ai_crawler", Op: "eq", Value: "gptbot"})
	if err != nil {
		t.Fatal(err)
	}
	if !prog.Matches(edgeRuleMatchInputFor(ctx, "/", "GET")) {
		t.Fatal("released: ai_crawler did not match GPTBot")
	}
	m.aiCrawlersUnreleased = true
	if prog.Matches(edgeRuleMatchInputFor(ctx, "/", "GET")) {
		t.Fatal("unreleased: ai_crawler still matched")
	}
}
