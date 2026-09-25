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
	releaseID    string
	deploymentID string
}

type releaseAsyncMatcher struct{ noOpEdgeRuleMatcher }

func (releaseAsyncMatcher) MatchAsync(context.Context, string, string, string) *EdgeRuleAsyncResolved {
	return &EdgeRuleAsyncResolved{ID: "async-release-test"}
}

func (b *releaseBackend) ResolveProjectRelease(_ context.Context, appID, scope, requestedID string) (string, string, error) {
	if appID != b.app.ID || scope != "production" || requestedID != "" && requestedID != b.releaseID {
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
