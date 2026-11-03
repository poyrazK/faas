// adr: 089, 1191 — a source app's own "@app" route rule applies on its own
// host on the cached path (#6148).
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// appPinRouteMatcher holds the source app's "@app" route rule only in its
// (host, app) pin, as the cached gatewayd-internal matcher does: the host pin
// taken before the owner is known carries the host's own rules alone.
type appPinRouteMatcher struct {
	stubEdgeRuleMatcher
	app     string
	route   *EdgeRuleResolved
	pinFail bool
}

func (m appPinRouteMatcher) PinHostPolicy(ctx context.Context, host string) (context.Context, error) {
	if m.pinFail && EdgeRuleApp(ctx) == m.app {
		return nil, errors.New("app pin unavailable")
	}
	entry := &HostEntry{Host: host}
	if err := entry.SealPolicy(); err != nil {
		return nil, err
	}
	return WithPinnedHostPolicy(ctx, EdgeRuleCacheKey(host, EdgeRuleApp(ctx)), entry)
}

func (m appPinRouteMatcher) MatchRoute(ctx context.Context, host, _, _ string) *EdgeRuleResolved {
	if _, pinned := PinnedHostPolicy(ctx, EdgeRuleCacheKey(host, m.app)); !pinned || EdgeRuleApp(ctx) != m.app {
		return nil
	}
	return m.route
}

func TestRouteRuleOnTheSourceAppsOwnHostApplies(t *testing.T) {
	h, b, _ := newTestHandler(t)
	target := App{ID: "app-target", AccountID: b.app.AccountID, Slug: "target", Plan: api.PlanPro}
	h.WithEdgeRules(appPinRouteMatcher{app: b.app.ID, route: &EdgeRuleResolved{
		ID: "rule-route", AccountID: b.app.AccountID, AppID: b.app.ID, TargetAppSlug: "target",
	}}, func(context.Context, string) (App, bool) { return target, true }, &captureAuditor{})
	req := httptest.NewRequest(http.MethodGet, "http://"+b.host+"/status", nil)
	req.Header.Set("X-Forwarded-For", "192.0.2.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.preparedRouteTargets) != 1 || b.preparedRouteTargets[0] != target.ID {
		t.Fatalf("prepared route targets = %v, want [%s]: the app's own route rule did not apply", b.preparedRouteTargets, target.ID)
	}
}

// A source app's pin that cannot load refuses the request instead of serving
// the source app as if it had no route rule.
func TestRouteRuleSourceAppPinFailureFailsClosed(t *testing.T) {
	h, b, _ := newTestHandler(t)
	target := App{ID: "app-target", AccountID: b.app.AccountID, Slug: "target", Plan: api.PlanPro}
	h.WithEdgeRules(appPinRouteMatcher{app: b.app.ID, pinFail: true, route: &EdgeRuleResolved{
		ID: "rule-route", AccountID: b.app.AccountID, AppID: b.app.ID, TargetAppSlug: "target",
	}}, func(context.Context, string) (App, bool) { return target, true }, &captureAuditor{})
	req := httptest.NewRequest(http.MethodGet, "http://"+b.host+"/status", nil)
	req.Header.Set("X-Forwarded-For", "192.0.2.1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || b.admits != 0 {
		t.Fatalf("source app pin failure = %d (admits %d), want 503 before wake: %s", rec.Code, b.admits, rec.Body)
	}
}
