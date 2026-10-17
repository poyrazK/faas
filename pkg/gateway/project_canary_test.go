package gateway

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"testing"
)

type canaryBackend struct {
	*releaseBackend
	calls                                 int
	key                                   string
	candidateRelease, candidateDeployment string
}

func (b *canaryBackend) ResolveProjectCanary(_ context.Context, _, _, key string) (string, string, error) {
	b.calls++
	b.key = key
	return b.candidateRelease, b.candidateDeployment, nil
}

func TestProjectCanaryIngressAndExplicitPin(t *testing.T) {
	h, backend, upstream := newTestHandler(t)
	oldDep, newDep, oldRelease, newRelease := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	backend.app.ProjectID = uuid.NewString()
	backend.app.RevisionPinTTLSeconds = 3600
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "old", DeploymentID: oldDep})
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "new", DeploymentID: newDep})
	canary := &canaryBackend{releaseBackend: &releaseBackend{fakeBackend: backend, releaseID: oldRelease, deploymentID: oldDep}, candidateRelease: newRelease, candidateDeployment: newDep}
	h.backend = canary
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/checkout", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != 200 || response.Header().Get(api.ReleaseHeader) != newRelease || response.Header().Get(api.RevisionHeader) != newDep || canary.calls != 1 {
		t.Fatalf("cohort not used: %d %v", response.Code, response.Header())
	}
	if _, err := uuid.Parse(canary.key); err != nil {
		t.Fatal("cohort key was not generated at ingress")
	}
	req = httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/checkout", nil)
	req.Header.Set(api.ReleaseHeader, oldRelease)
	response = httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != 200 || response.Header().Get(api.ReleaseHeader) != oldRelease || canary.calls != 1 {
		t.Fatal("explicit graph pin was redistributed")
	}
}
