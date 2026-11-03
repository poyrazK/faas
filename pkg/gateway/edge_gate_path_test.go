package gateway

import (
	"context"
	"net/http/httptest"
	"testing"
)

// adr: 091 — an access gate matches the path the client sent first and the
// rewritten path second (amendment 2026-11-02, review round 8).
func TestMatchEdgeGateTriesTheClientPathThenTheRewrittenOne(t *testing.T) {
	type rule struct{ path string }
	rules := map[string]*rule{"/api/users": {"/api/users"}, "/internal/users": {"/internal/users"}}
	match := func(_ context.Context, path string) *rule { return rules[path] }

	r := httptest.NewRequest("GET", "/api/users", nil)
	r = withLifecycleRequestRoute(r, r.URL.Path, r.Method)
	r.URL.Path = "/users" // a rewrite took /api away
	if got := matchEdgeGate(r, match); got == nil || got.path != "/api/users" {
		t.Fatalf("gate after rewrite = %+v, want the rule for the client's /api/users", got)
	}

	r = httptest.NewRequest("GET", "/legacy/users", nil)
	r = withLifecycleRequestRoute(r, r.URL.Path, r.Method)
	r.URL.Path = "/internal/users"
	if got := matchEdgeGate(r, match); got == nil || got.path != "/internal/users" {
		t.Fatalf("gate written for the rewritten path = %+v, want it to keep applying", got)
	}

	r = httptest.NewRequest("GET", "/other", nil)
	if got := matchEdgeGate(r, match); got != nil {
		t.Fatalf("no rule = %+v, want nil", got)
	}
}
