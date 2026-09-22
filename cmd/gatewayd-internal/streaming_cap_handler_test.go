package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type stubStreamingCapMatcher struct {
	rule *gateway.EdgeRuleLimitResolved
}

func (s stubStreamingCapMatcher) MatchLimit(context.Context, string, string, string) *gateway.EdgeRuleLimitResolved {
	return s.rule
}

func TestInternalStreamingCapHandlerReturnsEndpointOverride(t *testing.T) {
	matcher := stubStreamingCapMatcher{rule: &gateway.EdgeRuleLimitResolved{
		ID:                    "rule-1",
		AppID:                 "app-1",
		MaxBodyBytesStreaming: 12 << 20,
	}}
	lookup := gateway.ResolveSlugFn(func(slug string) (string, bool) {
		return map[string]string{"demo": "app-1"}[slug], slug == "demo"
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/apps/demo/streaming-cap?host=demo.example&path=%2Fevents&method=get", nil)
	rec := httptest.NewRecorder()
	internalStreamingCapHandler(matcher, lookup, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got streamingCapResponseJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Override || got.AppID != "app-1" || got.RuleID != "rule-1" || got.MaxBodyBytesStreaming != 12<<20 {
		t.Fatalf("response=%+v, want endpoint override", got)
	}
}

func TestInternalStreamingCapHandlerRejectsCrossAppRule(t *testing.T) {
	matcher := stubStreamingCapMatcher{rule: &gateway.EdgeRuleLimitResolved{
		ID:                    "other-rule",
		AppID:                 "other-app",
		MaxBodyBytesStreaming: 12 << 20,
	}}
	lookup := gateway.ResolveSlugFn(func(string) (string, bool) { return "app-1", true })
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/apps/demo/streaming-cap?host=demo.example&path=%2Fevents&method=GET", nil)
	rec := httptest.NewRecorder()
	internalStreamingCapHandler(matcher, lookup, nil).ServeHTTP(rec, req)

	var got streamingCapResponseJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Override || got.AppID != "app-1" {
		t.Fatalf("response=%+v, want plan fallback", got)
	}
}

func TestInternalStreamingCapHandlerRequiresCompleteRequestShape(t *testing.T) {
	lookup := gateway.ResolveSlugFn(func(string) (string, bool) { return "app-1", true })
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/apps/demo/streaming-cap?path=%2Fevents&method=GET", nil)
	rec := httptest.NewRecorder()
	internalStreamingCapHandler(stubStreamingCapMatcher{}, lookup, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}
