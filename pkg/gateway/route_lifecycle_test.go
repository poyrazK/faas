package gateway

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/routelifecycle"
	"net/http/httptest"
	"testing"
	"time"
)

type lifecycleMatcher struct {
	err          error
	path, method string
}

func (m *lifecycleMatcher) MatchDeclaredRoute(context.Context, App, string, string) (bool, error) {
	return true, nil
}
func (m *lifecycleMatcher) ResolveRouteLifecycle(_ context.Context, _ App, path, method string) (routelifecycle.Metadata, error) {
	m.path, m.method = path, method
	return routelifecycle.Metadata{DeprecatedAt: time.Unix(100, 0)}, m.err
}
func TestGatewayRouteLifecycle(t *testing.T) {
	matcher := &lifecycleMatcher{}
	handler := &Handler{declaredRoutes: matcher}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/rewritten", nil)
	handler.applyRouteLifecycle(w, r, App{ID: "app"}, "/public/old", "POST")
	if w.Header().Get("Deprecation") != "@100" || matcher.path != "/public/old" || matcher.method != "POST" {
		t.Fatalf("headers=%v matcher=%+v", w.Header(), matcher)
	}
	matcher.err = errors.New("unavailable")
	w = httptest.NewRecorder()
	handler.applyRouteLifecycle(w, r, App{}, "/public/old", "POST")
	if w.Header().Get("Deprecation") != "" {
		t.Fatal("failed resolution emitted metadata")
	}
}

type deploymentLifecycleMatcher struct {
	lifecycleMatcher
	selected string
}

func (m *deploymentLifecycleMatcher) ResolveDeploymentRouteLifecycle(_ context.Context, _ App, id, path, method string) (routelifecycle.Metadata, error) {
	m.selected = id
	m.path, m.method = path, method
	return routelifecycle.Metadata{DeprecatedAt: time.Unix(200, 0)}, m.err
}
func TestGatewaySelectedDeploymentLifecycle(t *testing.T) {
	matcher := &deploymentLifecycleMatcher{}
	handler := &Handler{declaredRoutes: matcher}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/rewrite", nil)
	handler.applyDeploymentRouteLifecycle(w, r, App{PinnedDeploymentID: "baseline"}, "canary", "/public", "HEAD")
	if matcher.selected != "canary" || matcher.path != "/public" || matcher.method != "HEAD" || w.Header().Get("Deprecation") != "@200" {
		t.Fatalf("selected %+v headers %v", matcher, w.Header())
	}
	matcher.err = errors.New("missing capture")
	w = httptest.NewRecorder()
	handler.applyDeploymentRouteLifecycle(w, r, App{}, "canary", "/public", "HEAD")
	if w.Header().Get("Deprecation") != "" {
		t.Fatal("failed lookup emitted headers")
	}
}
func TestCachedLifecycleHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		excluded    bool
	}{{"Deprecation", "@200", true}, {"Sunset", "date", true}, {"Link", "<https://example.com>; rel=\"successor-version\"", true}, {"Link", "<https://example.com/help>; rel=\"help\"", false}, {"Content-Type", "application/json", false}} {
		if isCachedRouteLifecycleHeader(tc.name, tc.value) != tc.excluded {
			t.Fatalf("%+v", tc)
		}
	}
}

func TestDeploymentLifecycleOriginAndCacheIsolation(t *testing.T) {
	matcher := &deploymentLifecycleMatcher{}
	h := &Handler{declaredRoutes: matcher}
	recorder := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/public", nil)
	r = h.applyDeploymentRouteLifecycle(recorder, r, App{}, "selected", "/public", "GET")
	forwardedResponseHeader(r.Context(), recorder.Header(), "Deprecation", "@999")
	forwardedResponseHeader(r.Context(), recorder.Header(), "Link", "<https://example.com/help>; rel=\"help\"")
	if recorder.Header().Get("Deprecation") != "@200" || len(recorder.Header().Values("Deprecation")) != 1 {
		t.Fatal("guest overwrote selected metadata")
	}
	rule := &EdgeRuleCacheResolved{ID: "rule", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(recorder)
	writer := newCacheWriter(rec, rec, rule, ResponseCachePerEntryMaxBytes)
	writer.WriteHeader(200)
	if writer.header.Get("Deprecation") != "" || writer.header.Get("Link") == "" {
		t.Fatalf("cached headers %v", writer.header)
	}
	if recorder.Header().Get("Deprecation") != "@200" {
		t.Fatal("cache filtering changed live response")
	}
}
