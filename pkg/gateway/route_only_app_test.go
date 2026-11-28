// adr: 20261121170000
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Amendment 6: a route split's route app answers only requests its source
// app's route rule sends it, whatever host reached it.
func TestRouteAppServesOnlyThroughItsSourcesRoute(t *testing.T) {
	refused := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "Not served here")
	}
	t.Run("direct request", func(t *testing.T) {
		h, b, _ := newTestHandler(t)
		b.app.RouteOfAppID = "app-source"
		req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !refused(rec) {
			t.Fatalf("direct request to a route app = %d %s, want refused", rec.Code, rec.Body)
		}
	})
	for _, tc := range []struct {
		name    string
		routeOf func(sourceID string) string
		refused bool
	}{
		{"routed by its source", func(sourceID string) string { return sourceID }, false},
		{"routed by another app", func(string) string { return "app-elsewhere" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			target := App{ID: "app-target", AccountID: "acct-1", Slug: "target", Plan: api.PlanPro, RouteOfAppID: tc.routeOf(b.app.ID)}
			h.WithEdgeRules(routeRuleMatcher{route: &EdgeRuleResolved{ID: "rule-route", AccountID: "acct-1", TargetAppSlug: "target"}},
				func(context.Context, string) (App, bool) { return target, true }, &captureAuditor{})
			req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
			req.Header.Set("X-Forwarded-For", "192.0.2.1")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if refused(rec) != tc.refused {
				t.Fatalf("status = %d %s, want refused=%v", rec.Code, rec.Body, tc.refused)
			}
		})
	}
}

// The rollout's authorized smoke reaches a route app on its own host, or
// the route's app could never go live.
func TestDeploymentSmokeReachesARouteApp(t *testing.T) {
	f := newSmokeCacheFixture(t)
	f.backend.fakeBackend.app.RouteOfAppID = "app-source"
	for _, smoke := range []bool{false, true} {
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, f.request(smoke))
		if smoke && rec.Code != http.StatusOK {
			t.Fatalf("authorized smoke to a route app: status %d %s, want 200", rec.Code, rec.Body)
		}
		if !smoke && rec.Code != http.StatusNotFound {
			t.Fatalf("direct request to a route app: status %d, want 404", rec.Code)
		}
	}
}
