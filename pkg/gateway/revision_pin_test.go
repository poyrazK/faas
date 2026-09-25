package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type revisionPinBackend struct {
	*fakeBackend
	allowed string
}

func (b *revisionPinBackend) ResolveRevisionPin(_ context.Context, appID, scope, deploymentID string) (bool, error) {
	return appID == b.app.ID && scope == b.app.Scope && deploymentID == b.allowed, nil
}

func TestPublicRevisionPinSelectsExactDeployment(t *testing.T) {
	h, backend, upstream := newTestHandler(t)
	oldID, newID := uuid.NewString(), uuid.NewString()
	backend.app.RevisionPinTTLSeconds = 3600
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "new", DeploymentID: newID})
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "old", DeploymentID: oldID})
	pinnedBackend := &revisionPinBackend{fakeBackend: backend, allowed: oldID}
	h.backend = pinnedBackend

	request := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/checkout", nil)
	request.Header.Set(api.RevisionHeader, oldID)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get(api.RevisionHeader) != oldID {
		t.Fatalf("pinned response = %d revision %q body %q", response.Code, response.Header().Get(api.RevisionHeader), response.Body.String())
	}
	pinnedBackend.allowed = ""
	request = httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/checkout", nil)
	request.Header.Set(api.RevisionHeader, oldID)
	response = httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusGone {
		t.Fatalf("expired response = %d %q", response.Code, response.Body.String())
	}
}
