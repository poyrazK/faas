// adr: 20261101141123 — a pending edge-rule change does not refuse its hosts;
// only a route rule whose target stopped resolving fails closed meanwhile.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type convergingEdgeRuleMatcher struct{ noOpEdgeRuleMatcher }

func (convergingEdgeRuleMatcher) Converging(string, string, string) bool { return true }

func TestHandlerServesDuringEdgeRuleConvergence(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	h.edgeRules = convergingEdgeRuleMatcher{}

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "Edge policy update in progress") {
		t.Fatalf("a pending change refused the request: %d %s", rec.Code, rec.Body.String())
	}
	if backend.pickCalls.Load() == 0 && backend.admits == 0 {
		t.Fatalf("request never reached the backend during convergence: %d %s", rec.Code, rec.Body.String())
	}
}

// convergingRouteMatcher serves the source app's route rule and reports
// whether a change to its host is pending.
type convergingRouteMatcher struct {
	appPinRouteMatcher
	converging bool
}

func (m convergingRouteMatcher) Converging(string, string, string) bool { return m.converging }

// An app rename commits before its retargeted route rules apply, so a cached
// rule can name a slug that no longer resolves. While the change is pending,
// that request fails closed instead of running on the source app; with no
// change pending the cached path keeps its clean miss.
func TestRouteTargetMissDuringConvergence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		converging bool
		wantRefuse bool
	}{
		{"pending change refuses", true, true},
		{"no pending change falls through", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			h.WithEdgeRules(convergingRouteMatcher{converging: tc.converging, appPinRouteMatcher: appPinRouteMatcher{
				app: b.app.ID, route: &EdgeRuleResolved{ID: "rule-route", AccountID: b.app.AccountID, AppID: b.app.ID, TargetAppSlug: "renamed"},
			}}, func(context.Context, string) (App, bool) { return App{}, false }, &captureAuditor{})
			req := httptest.NewRequest(http.MethodGet, "http://"+b.host+"/status", nil)
			req.Header.Set("X-Forwarded-For", "192.0.2.1")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			refused := rec.Code == http.StatusServiceUnavailable && strings.Contains(rec.Body.String(), api.CodeTrafficPolicyUnavailable)
			if refused != tc.wantRefuse {
				t.Fatalf("status = %d body = %s, want refused=%v", rec.Code, rec.Body.String(), tc.wantRefuse)
			}
			if tc.wantRefuse && (b.admits != 0 || rec.Header().Get("Retry-After") != "1") {
				t.Fatalf("refusal admitted %d or Retry-After = %q, want 0 and 1", b.admits, rec.Header().Get("Retry-After"))
			}
		})
	}
}
