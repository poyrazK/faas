package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func mustTemplate(t *testing.T, s string, mode api.EdgeRuleTemplateMode) *api.EdgeRuleTemplate {
	t.Helper()
	c, err := api.CompileEdgeRuleTemplate(s, mode)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func templateTestRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-Tenant", "acme")
	m := NewEdgeRuleMatchContext(req, net.ParseIP("203.0.113.9"), func(net.IP) string { return "DE" }, nil)
	m.SetASNLookup(func(net.IP) uint32 { return 13335 })
	return req.WithContext(WithEdgeRuleMatchContext(context.Background(), m))
}

// adr: 967 — templated request and response header ops render request
// values; literal ops are untouched.
func TestEdgeRuleHeaderTemplatesApply(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.edgeRules = stubEdgeRuleMatcher{headers: &EdgeRuleHeadersResolved{
		ID: "hdr", AccountID: "acct-1", AppID: "app-1",
		RequestHeaders: []EdgeRuleHeaderOp{
			{Name: "X-Client-Country", Action: "set", Template: mustTemplate(t, "${country}", api.EdgeRuleTemplateHeader)},
			{Name: "X-Static", Action: "set", Value: "${country}"},
		},
		ResponseHeaders: []EdgeRuleHeaderOp{
			{Name: "X-Served-For", Action: "set", Template: mustTemplate(t, "${header:x-tenant}@AS${asn}", api.EdgeRuleTemplateHeader)},
		},
	}}
	req := templateTestRequest("http://api.example.com/x")
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: req}
	if !h.applyEdgeRuleHeaders(rec, req, App{ID: "app-1", AccountID: "acct-1"}, rec) {
		t.Fatal("headers rule did not apply")
	}
	if got := req.Header.Get("X-Client-Country"); got != "DE" {
		t.Fatalf("request header = %q, want DE", got)
	}
	if got := req.Header.Get("X-Static"); got != "${country}" {
		t.Fatalf("untemplated value changed: %q", got)
	}
	rec.WriteHeader(http.StatusOK)
	if got := w.Header().Get("X-Served-For"); got != "acme@AS13335" {
		t.Fatalf("response header = %q", got)
	}
}

// adr: 967 — a templated redirect renders its target from the request.
func TestEdgeRuleRedirectTemplateApply(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.edgeRules = stubEdgeRuleMatcher{redirect: &EdgeRuleRedirectResolved{
		ID: "redir", AccountID: "acct-1", AppID: "app-1", StatusCode: http.StatusPermanentRedirect,
		To:              "https://new.example${path}?${query}",
		ToTemplate:      mustTemplate(t, "https://new.example${path}?${query}", api.EdgeRuleTemplateRedirect),
		HeaderTemplates: map[string]*api.EdgeRuleTemplate{"X-From": mustTemplate(t, "${host}", api.EdgeRuleTemplateHeader)},
	}}
	req := templateTestRequest("http://old.example/docs/a%20b?lang=tr")
	w := httptest.NewRecorder()
	if !h.matchAndApplyRedirect(w, req, App{ID: "app-1", AccountID: "acct-1"}) {
		t.Fatal("redirect did not apply")
	}
	if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "https://new.example/docs/a%20b?lang=tr" {
		t.Fatalf("redirect = %d %q", w.Code, w.Header().Get("Location"))
	}
	if got := w.Header().Get("X-From"); got != "old.example" {
		t.Fatalf("templated redirect header = %q", got)
	}
}
