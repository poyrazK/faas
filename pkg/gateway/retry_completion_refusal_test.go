// adr: 375
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

func TestPublicRetryCompletionRefusedReplay(t *testing.T) {
	for _, name := range []string{"security", "canceled", "streaming"} {
		t.Run(name, func(t *testing.T) {
			app := App{ID: uuid.NewString(), AccountID: uuid.NewString()}
			original := Target{AppID: app.ID, InstanceID: "first", NodeID: "first-node", DeploymentID: uuid.NewString(), WakeID: "causal"}
			sibling := Target{AppID: app.ID, InstanceID: "sibling", NodeID: "sibling-node", DeploymentID: uuid.NewString(), WakeID: "historical"}
			backend := &fakeBackend{app: app}
			backend.AddTarget(original)
			backend.AddTarget(sibling)
			backend.Pick(app.ID)
			store := &gatewaySecurityStore{}
			if name == "security" {
				store.set(trafficrevocation.Scope{Kind: "deployment", ID: sibling.DeploymentID}, 1, true)
			}
			registry := trafficrevocation.New(store)
			defer registry.Close()
			request := httptest.NewRequest(http.MethodGet, "http://app.test/work", nil)
			ctx, cleanup, err := AdmitSyntheticTraffic(request.Context(), registry, trafficrevocation.Scope{Kind: "deployment", ID: original.DeploymentID})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			request = request.WithContext(ctx)
			h := NewHandlerWith(backend, nil, nil).WithTrafficRevocations(registry).
				WithRetryEnabled(true).WithRetryDefault(RetryPolicy{MaxAttempts: 2})
			response := httptest.NewRecorder()
			forwards := 0
			completed := h.proxyAttempt(response, request, original, name == "streaming", func(Target) {},
				func(w http.ResponseWriter, r *http.Request, target Target) {
					forwards++
					if target.InstanceID != original.InstanceID {
						t.Errorf("refused sibling forwarded: %+v", target)
					}
					markStaleTarget(r.Context())
					if name == "canceled" {
						cancel()
					}
					w.WriteHeader(http.StatusServiceUnavailable)
				}, app)
			if forwards != 1 || completed.InstanceID != original.InstanceID || completed.NodeID != original.NodeID || completed.WakeID != original.WakeID {
				t.Fatalf("forwards=%d completion=%+v want original cause and target", forwards, completed)
			}
			if name == "security" && (response.Code != http.StatusForbidden || backend.pickCalls.Load() != 2) {
				t.Errorf("security refusal status=%d picks=%d", response.Code, backend.pickCalls.Load())
			}
			cleanup()
			if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
				t.Errorf("leaked security registrations=%d/%d", exchanges, scopes)
			}
		})
	}
}
