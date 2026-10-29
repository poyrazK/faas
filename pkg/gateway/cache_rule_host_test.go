package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// cacheHostRecorder records the host the cache rule lookup was asked for.
type cacheHostRecorder struct {
	noOpEdgeRuleMatcher
	host string
}

func (m *cacheHostRecorder) MatchCache(_ context.Context, host, _, _ string) *EdgeRuleCacheResolved {
	m.host = host
	return nil
}

// adr: 091 — amendment 2026-10-28 (review round 3): cache rules are looked
// up by the host without its port, like every other kind. "Host:
// shop.example.com:443" missed the policy pinned for shop.example.com and its
// exact-host cache rules.
func TestCacheRuleLookupUsesTheHostWithoutPort(t *testing.T) {
	matcher := &cacheHostRecorder{}
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithEdgeRules(matcher, nil, nil)
	h.WithResponseCache(NewResponseCache())
	req := httptest.NewRequest(http.MethodGet, "http://shop.example.com/catalog", nil)
	req.Host = "Shop.Example.com:443"
	h.applyEdgeRuleCache(httptest.NewRecorder(), req, App{ID: "app-1", Slug: "shop"}, &statusRecorder{})
	if matcher.host != "shop.example.com" {
		t.Fatalf("cache rules looked up for %q, want shop.example.com", matcher.host)
	}
}
