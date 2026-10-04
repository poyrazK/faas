// adr: 569
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPreparingEnvironmentDoesNotWakeOrProxy(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.EnvironmentNotReady = true
	b.running = true // A production warm target must never satisfy this stage.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+b.host+"/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "Environment is not ready") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&b.admits) != 0 || b.pickCalls.Load() != 0 {
		t.Fatal("preparing stage admitted or picked a production target")
	}
}

func TestPreparingEnvironmentCannotEscapeThroughRouteRule(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.EnvironmentNotReady = true
	var resolved int
	h.WithEdgeRules(routeRuleMatcher{route: &EdgeRuleResolved{
		ID: "production-rewrite", AccountID: b.app.AccountID, TargetAppSlug: "production",
	}}, func(context.Context, string) (App, bool) {
		resolved++
		return App{ID: "production", AccountID: b.app.AccountID}, true
	}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+b.host+"/", nil))
	if rec.Code != http.StatusServiceUnavailable || resolved != 0 || b.pickCalls.Load() != 0 {
		t.Fatalf("response = %d, route resolutions = %d, picks = %d", rec.Code, resolved, b.pickCalls.Load())
	}
}
