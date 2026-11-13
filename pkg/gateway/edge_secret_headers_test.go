package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ADR-20261110210000 slice 3i: a sealed request header op is recorded for
// vmmd, not applied; literal ops still apply, and the request counts as
// credentialed for the shared cache.
func TestEdgeRuleHeadersRecordSealedOps(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.edgeRules = stubEdgeRuleMatcher{headers: &EdgeRuleHeadersResolved{
		ID: "hdr", AccountID: "acct-1", AppID: "app-1",
		RequestHeaders: []EdgeRuleHeaderOp{
			{Name: "Authorization", Action: "set", SecretAppID: "app-1", SecretID: "s-1"},
			{Name: "X-Static", Action: "set", Value: "literal"},
		},
	}}
	req := templateTestRequest("http://api.example.com/x")
	req.Header.Set("Authorization", "Bearer client")
	if hasCacheCredentials(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("a plain request is credentialed")
	}
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: req}
	if !h.applyEdgeRuleHeaders(rec, req, App{ID: "app-1", AccountID: "acct-1"}, rec) {
		t.Fatal("headers rule did not apply")
	}
	if got := req.Header.Values("Authorization"); len(got) != 0 {
		t.Fatalf("Authorization = %v, want none until vmmd adds the sealed value", got)
	}
	if req.Header.Get("X-Static") != "literal" {
		t.Fatal("literal op not applied")
	}
	refs := edgeSecretHeadersFrom(req.Context())
	if len(refs) != 1 || refs[0].GetName() != "Authorization" || refs[0].GetSecretAppId() != "app-1" || refs[0].GetSecretId() != "s-1" {
		t.Fatalf("recorded = %v", refs)
	}
	if !hasCacheCredentials(req) {
		t.Fatal("a request carrying a sealed rule header is not credentialed for the shared cache")
	}
}
