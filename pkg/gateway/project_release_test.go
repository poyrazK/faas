// adr: 259
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type releaseBackend struct {
	*fakeBackend
	releaseID          string
	deploymentID       string
	activeReleaseID    string
	releaseDeployments map[string]string
}

type releaseAsyncMatcher struct{ noOpEdgeRuleMatcher }

func (releaseAsyncMatcher) MatchAsync(context.Context, string, string, string) *EdgeRuleAsyncResolved {
	return &EdgeRuleAsyncResolved{ID: "async-release-test"}
}

func (b *releaseBackend) ResolveProjectRelease(_ context.Context, appID, scope, requestedID string) (string, string, error) {
	if appID != b.app.ID || scope != "production" {
		return "", "", ErrReleaseGone
	}
	if b.releaseDeployments != nil {
		if requestedID == "" {
			requestedID = b.activeReleaseID
		}
		deploymentID, ok := b.releaseDeployments[requestedID]
		if !ok {
			return "", "", ErrReleaseGone
		}
		return requestedID, deploymentID, nil
	}
	if requestedID != "" && requestedID != b.releaseID {
		return "", "", ErrReleaseGone
	}
	return b.releaseID, b.deploymentID, nil
}

func TestPublicProjectReleaseSelectsExactMember(t *testing.T) {
	h, backend, upstream := newTestHandler(t)
	newID, oldID, releaseID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	backend.app.ProjectID = uuid.NewString()
	backend.app.RevisionPinTTLSeconds = 3600
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "new", DeploymentID: newID})
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "old", DeploymentID: oldID})
	h.backend = &releaseBackend{fakeBackend: backend, releaseID: releaseID, deploymentID: oldID}
	request := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/checkout", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get(api.ReleaseHeader) != releaseID || response.Header().Get(api.RevisionHeader) != oldID {
		t.Fatalf("release response = %d release %q revision %q", response.Code, response.Header().Get(api.ReleaseHeader), response.Header().Get(api.RevisionHeader))
	}
}

func TestStaticSPADocumentSeedsReleaseContextCookie(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	releaseID, deploymentID := uuid.NewString(), uuid.NewString()
	backend.app.ProjectID = uuid.NewString()
	backend.app.RevisionPinTTLSeconds = 3600
	var guestCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guestCookie = r.Header.Get("Cookie")
		w.Header().Add("Set-Cookie", api.ManagedReleaseContextCookieName+"=guest-value; Path=/; Secure")
		w.Header().Add("Set-Cookie", "session=guest; Path=/")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>SPA</title>"))
	}))
	t.Cleanup(upstream.Close)
	backend.upstream = upstream.Listener.Addr().String()
	backend.AddTarget(Target{NodeID: backend.upstream, InstanceID: "spa", DeploymentID: deploymentID})
	h.backend = &releaseBackend{fakeBackend: backend, releaseID: releaseID, deploymentID: deploymentID}

	request := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Sec-Fetch-Dest", "document")
	request.Header.Set("Cookie", api.ManagedReleaseContextCookieName+"="+uuid.NewString()+"; session=keep")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get(api.ReleaseHeader) != releaseID {
		t.Fatalf("document response = %d release %q body %q", response.Code, response.Header().Get(api.ReleaseHeader), response.Body.String())
	}
	var releaseCookie *http.Cookie
	var releaseCookieCount, sessionCookieCount int
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == api.ManagedReleaseContextCookieName {
			releaseCookieCount++
			releaseCookie = cookie
		} else if cookie.Name == "session" {
			sessionCookieCount++
		}
	}
	if releaseCookieCount != 1 || sessionCookieCount != 1 || releaseCookie == nil || releaseCookie.Value != releaseID || releaseCookie.Path != "/" ||
		!releaseCookie.Secure || releaseCookie.HttpOnly || releaseCookie.SameSite != http.SameSiteLaxMode ||
		releaseCookie.MaxAge != 0 {
		t.Fatalf("release context cookie = %+v", releaseCookie)
	}
	if guestCookie != "session=keep" {
		t.Fatalf("guest received platform release cookie or lost app cookie: %q", guestCookie)
	}
}

func TestStaticSPADocumentClearsReleaseCookieWithoutActiveGraph(t *testing.T) {
	h, backend, upstream := newTestHandler(t)
	backend.app.ProjectID = uuid.NewString()
	backend.app.RevisionPinTTLSeconds = 3600
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "current", DeploymentID: uuid.NewString()})
	h.backend = &releaseBackend{fakeBackend: backend}

	request := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Sec-Fetch-Dest", "document")
	request.AddCookie(&http.Cookie{Name: api.ManagedReleaseContextCookieName, Value: uuid.NewString()})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get(api.ReleaseHeader) != "" {
		t.Fatalf("unpinned document response = %d release %q", response.Code, response.Header().Get(api.ReleaseHeader))
	}
	var cleared bool
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == api.ManagedReleaseContextCookieName && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("document without a graph did not clear the release cookie: %q", response.Header().Values("Set-Cookie"))
	}
}

func TestPublicProjectReleaseClientSticksAcrossCutoverAndExpires(t *testing.T) {
	h, backend, upstream := newTestHandler(t)
	oldReleaseID, newReleaseID := uuid.NewString(), uuid.NewString()
	oldDeploymentID, newDeploymentID := uuid.NewString(), uuid.NewString()
	backend.app.ProjectID = uuid.NewString()
	backend.app.RevisionPinTTLSeconds = 3600
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "old", DeploymentID: oldDeploymentID})
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "new", DeploymentID: newDeploymentID})
	resolver := &releaseBackend{fakeBackend: backend, activeReleaseID: oldReleaseID, releaseDeployments: map[string]string{
		oldReleaseID: oldDeploymentID,
		newReleaseID: newDeploymentID,
	}}
	h.backend = resolver

	requestRelease := func(pin string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/checkout", nil)
		if pin != "" {
			request.Header.Set(api.ReleaseHeader, pin)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		return response
	}

	// The initial page/API response carries the release ID the browser embeds
	// in its HTML bootstrap metadata.
	oldClientBootstrap := requestRelease("")
	if oldClientBootstrap.Code != http.StatusOK || oldClientBootstrap.Header().Get(api.ReleaseHeader) != oldReleaseID || oldClientBootstrap.Header().Get(api.RevisionHeader) != oldDeploymentID {
		t.Fatalf("old-client bootstrap = %d release %q revision %q", oldClientBootstrap.Code, oldClientBootstrap.Header().Get(api.ReleaseHeader), oldClientBootstrap.Header().Get(api.RevisionHeader))
	}

	// Publish a new graph. Unpinned clients move to it, while the client that
	// bootstrapped with the old release stays on the exact old deployment.
	resolver.activeReleaseID = newReleaseID
	newClient := requestRelease("")
	oldClient := requestRelease(oldReleaseID)
	if newClient.Code != http.StatusOK || newClient.Header().Get(api.ReleaseHeader) != newReleaseID || newClient.Header().Get(api.RevisionHeader) != newDeploymentID {
		t.Fatalf("new-client request = %d release %q revision %q", newClient.Code, newClient.Header().Get(api.ReleaseHeader), newClient.Header().Get(api.RevisionHeader))
	}
	if oldClient.Code != http.StatusOK || oldClient.Header().Get(api.ReleaseHeader) != oldReleaseID || oldClient.Header().Get(api.RevisionHeader) != oldDeploymentID {
		t.Fatalf("old-client pinned request = %d release %q revision %q", oldClient.Code, oldClient.Header().Get(api.ReleaseHeader), oldClient.Header().Get(api.RevisionHeader))
	}

	// Removing the retired set models TTL expiry. The old client gets 410 and
	// is never silently routed into the new graph.
	delete(resolver.releaseDeployments, oldReleaseID)
	expiredClient := requestRelease(oldReleaseID)
	if expiredClient.Code != http.StatusGone {
		t.Fatalf("expired old-client request = %d, want %d", expiredClient.Code, http.StatusGone)
	}
}

func TestPublicProjectReleaseRejectsExpiredPinnedAsyncRoute(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.ProjectID = uuid.NewString()
	h.backend = &releaseBackend{fakeBackend: backend, releaseID: uuid.NewString(), deploymentID: uuid.NewString()}
	h.edgeRules = releaseAsyncMatcher{}
	request := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/checkout", nil)
	request.Header.Set(api.ReleaseHeader, uuid.NewString())
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusGone {
		t.Fatalf("expired pinned async route = %d, want 410", response.Code)
	}
}

func TestPublicProjectReleaseCarriesPinIntoAsyncRoute(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	releaseID, deploymentID := uuid.NewString(), uuid.NewString()
	backend.app.ProjectID = uuid.NewString()
	backend.app.RequestInvocationsEnabled = true
	h.backend = &releaseBackend{fakeBackend: backend, releaseID: releaseID, deploymentID: deploymentID}
	h.edgeRules = releaseAsyncMatcher{}
	enqueuer := &recordingAsyncRouteEnqueuer{}
	h.asyncRoutes = enqueuer
	request := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/checkout", nil)
	request.Header.Set(api.ReleaseHeader, releaseID)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || enqueuer.calls != 1 {
		t.Fatalf("pinned async route = %d, enqueue calls %d, body %s", response.Code, enqueuer.calls, response.Body.String())
	}
	if enqueuer.request.Headers[api.ReleaseHeader] != releaseID || response.Header().Get(api.ReleaseHeader) != releaseID {
		t.Fatalf("release did not reach queue and response: headers=%#v response=%q", enqueuer.request.Headers, response.Header().Get(api.ReleaseHeader))
	}
}

func TestPublicProjectReleaseRejectsMalformedID(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.ProjectID = uuid.NewString()
	h.backend = &releaseBackend{fakeBackend: backend, releaseID: uuid.NewString(), deploymentID: uuid.NewString()}
	request := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/checkout", nil)
	request.Header.Set(api.ReleaseHeader, "not-a-uuid-xxxxxxxxxxxxxxxxxxxxxxxxx")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed release ID = %d, want 400", response.Code)
	}
}

func TestServiceProxyReleaseUsesVerifiedCallerDeployment(t *testing.T) {
	releaseID := uuid.NewString()
	callerDeployment := uuid.NewString()
	oldTarget := uuid.NewString()
	newTarget := uuid.NewString()
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{
		{InstanceID: "new", NodeID: "node-a", DeploymentID: newTarget, Port: 8080},
		{InstanceID: "old", NodeID: "node-a", DeploymentID: oldTarget, Port: 8080},
	}}}
	var chosen, forwardedRelease, forwardedRevision string
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize:             func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		ResolveCallerIdentity: func(context.Context, string) (string, string, error) { return "app-api", callerDeployment, nil },
		ResolveRelease: func(_ context.Context, callerAppID, callerDepID, targetAppID, requestedID string) (string, string, error) {
			if callerAppID != "app-api" || callerDepID != callerDeployment || targetAppID != "app-orders" || requestedID != releaseID {
				return "", "", ErrReleaseGone
			}
			return releaseID, oldTarget, nil
		},
		// A graph target was already validated by ResolveRelease; the
		// one-hop override validator may reject it when direct pins are off.
		ValidateDeployment: func(context.Context, string, string) (bool, error) { return false, nil },
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				chosen, forwardedRelease, forwardedRevision = target.DeploymentID, r.Header.Get(api.ReleaseHeader), r.Header.Get(api.RevisionHeader)
				w.WriteHeader(http.StatusNoContent)
			})
		},
	})
	request := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/checkout", nil)
	request.Header.Set(api.ReleaseHeader, releaseID)
	request.Header.Set(api.RevisionHeader, callerDeployment)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || chosen != oldTarget || forwardedRelease != releaseID || forwardedRevision != "" {
		t.Fatalf("service response = %d target %q release %q revision %q body %q", response.Code, chosen, forwardedRelease, forwardedRevision, response.Body.String())
	}
}
