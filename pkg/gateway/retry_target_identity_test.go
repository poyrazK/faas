// adr: 375
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestPublicRetryRefreshesTargetIdentityWithoutMutatingPriorAttempt(t *testing.T) {
	app := App{ID: "app", AccountID: "account"}
	original := Target{AppID: app.ID, DeploymentID: "revision", InstanceID: "original", NodeID: "old-node", ImageDigest: "old-image", Region: "old-region"}
	sibling := Target{AppID: app.ID, DeploymentID: "revision", InstanceID: "sibling", NodeID: "new-node"}
	request := httptest.NewRequest(http.MethodGet, "http://app.test/work", nil)
	request.Header.Set(api.RequestIDHeader, "request")
	ctx := wire.WithContext(request.Context(), wire.CorrelationFields{WakeID: "wake", InvocationID: "invocation", TraceID: "trace"})
	request = request.WithContext(withAuthenticated(ctx, Authenticated{PlatformTenantID: "customer"}))
	first := requestForTarget(request.Context(), request, app, original)
	var replay *http.Request
	backend := &fakeBackend{app: app}
	backend.AddTarget(original)
	backend.AddTarget(sibling)
	if picked := backend.Pick(app.ID); !picked.OK || picked.Target.InstanceID != original.InstanceID {
		t.Fatal("fixture did not select the original target")
	}
	handler := NewHandlerWith(backend, nil, nil).WithRetryEnabled(true).WithRetryDefault(RetryPolicy{MaxAttempts: 2})
	response := httptest.NewRecorder()
	handler.proxyAttempt(response, first, original, false, func(Target) {},
		func(w http.ResponseWriter, r *http.Request, selected Target) {
			if selected.InstanceID == original.InstanceID {
				markStaleTarget(r.Context())
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			replay = r
			w.WriteHeader(http.StatusOK)
		}, app)
	if response.Code != http.StatusOK || replay == nil {
		t.Fatalf("replay did not reach sibling: status=%d replay=%v", response.Code, replay != nil)
	}
	for header, want := range map[string]string{
		api.InstanceIDHeader: sibling.InstanceID, "X-Faas-Instance": sibling.InstanceID,
		api.NodeIDHeader: sibling.NodeID, "X-Faas-Node": sibling.NodeID,
		api.AppIDHeader: app.ID, api.DeploymentIDHeader: sibling.DeploymentID,
		api.TenantIDHeader: app.AccountID, api.PlatformTenantIDHeader: "customer", api.RequestIDHeader: "request",
		api.ImageDigestHeader: "", api.RegionHeader: "",
	} {
		if got := replay.Header.Get(header); got != want {
			t.Errorf("replay %s=%q want=%q", header, got, want)
		}
	}
	fields, ok := wire.FromContext(replay.Context())
	if !ok || fields.InstanceID != sibling.InstanceID || fields.NodeID != sibling.NodeID || fields.AppID != app.ID ||
		fields.DeploymentID != sibling.DeploymentID || fields.ImageDigest != "" || fields.Region != "" ||
		fields.RequestID != "request" || fields.TenantID != app.AccountID || fields.WakeID != "wake" || fields.InvocationID != "invocation" || fields.TraceID != "trace" {
		t.Fatalf("replay correlation=%+v", fields)
	}
	if first.Header.Get(api.InstanceIDHeader) != original.InstanceID || first.Header.Get(api.ImageDigestHeader) != original.ImageDigest || request.Header.Get(api.InstanceIDHeader) != "" {
		t.Fatal("attempt stamping mutated the inbound or prior attempt's headers")
	}
}
