// adr: 196
//
// End-to-end coverage for the internal service mesh (ADR-167..170, ADR-196,
// ADR-197). Until now the mesh had unit tests only: nothing exercised the real
// path from a caller, through the node-local service proxy, across the vmmd
// bridge, to a target guest. That is precisely the gap that hid the dropped
// grpc-status trailer, so the boundary is worth holding down end to end.
//
// Scope: the loopback control-listener mount, which is CI-safe. The
// guest-facing tenant-bridge listener (HostBridgeIP:10080) and its DNS
// resolver need a real bridge address and are metal concerns.
//
// Build tag: (none). Requires Postgres (skip via FAAS_SKIP_PG_TESTS). No KVM.
package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// serviceCall issues an internal service-proxy request on the loopback control
// listener. callerAppID is empty to exercise the unauthenticated path.
func serviceCall(t *testing.T, h *e2etest.Harness, callerAppID, service, path string) (int, http.Header, string) {
	t.Helper()
	url := h.GatewayControlURL + "/v1/internal/services/" + service + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build service request: %v", err)
	}
	if callerAppID != "" {
		req.Header.Set("X-Faas-Caller-App", callerAppID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("service call: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

// createServiceApp adds a second app to the fixture's account — the target the
// caller reaches by name.
func createServiceApp(t *testing.T, f *normalPathFixture, slug string, patch map[string]any) api.AppResponse {
	t.Helper()
	body, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false)})
	if status != http.StatusCreated {
		t.Fatalf("create service app %q: status=%d body=%s", slug, status, body)
	}
	var app api.AppResponse
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode service app: %v body=%s", err, body)
	}
	if len(patch) > 0 {
		body, status = doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+slug, patch)
		if status != http.StatusOK {
			t.Fatalf("patch service app %q: status=%d body=%s", slug, status, body)
		}
	}
	return app
}

// pollServiceCall retries until the expected status lands or the budget
// expires. Wake and cache-invalidation are asynchronous at the edges, so a
// single shot would be testing timing rather than behaviour.
func pollServiceCall(t *testing.T, h *e2etest.Harness, callerAppID, service, path string, want int, budget time.Duration) (int, http.Header, string) {
	t.Helper()
	deadline := time.Now().Add(budget)
	var status int
	var header http.Header
	var body string
	for time.Now().Before(deadline) {
		status, header, body = serviceCall(t, h, callerAppID, service, path)
		if status == want {
			return status, header, body
		}
		time.Sleep(200 * time.Millisecond)
	}
	return status, header, body
}

// A warm same-account target is reached by name and the request arrives at the
// right instance with the path preserved.
func TestE2E_ServiceMesh_ForwardsToWarmTarget(t *testing.T) {
	f := newNormalPathFixture(t, "mesh-caller")
	if f == nil {
		return
	}
	target := createServiceApp(t, f, "meshorders", nil)
	_, instance := createNormalPathLiveDeployment(t, f, target.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")

	status, _, body := pollServiceCall(t, f.h, f.app.ID, "meshorders", "/health", http.StatusOK, 15*time.Second)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if !strings.Contains(body, "meshorders:v1") && !strings.Contains(body, "v1") {
		t.Errorf("body = %q, want the target guest's response", body)
	}

	req := f.vmmd.LastRequest()
	if req == nil {
		t.Fatal("fake vmmd received no ForwardHTTPStream request")
	}
	if req.Instance != instance.ID {
		t.Errorf("forwarded instance = %q, want %q", req.Instance, instance.ID)
	}
	if req.RequestUri != "/health" {
		t.Errorf("forwarded URI = %q, want /health", req.RequestUri)
	}
	// The platform-owned caller header must never reach the guest.
	for _, hdr := range req.Headers {
		if strings.EqualFold(hdr.GetName(), "x-faas-caller-app") {
			t.Errorf("caller header leaked to the guest: %s: %s", hdr.GetName(), hdr.GetValue())
		}
	}
}

// The ADR-196 headline: a parked internal service is woken by the call rather
// than 503'd. Without this an internal dependency can never scale to zero.
func TestE2E_ServiceMesh_WakesParkedTarget(t *testing.T) {
	f := newNormalPathFixture(t, "mesh-wake-caller")
	if f == nil {
		return
	}
	target := createServiceApp(t, f, "meshparked", nil)
	// The wake mints the instance id, so it cannot be registered with the
	// fake ahead of time; a default response stands in for the guest that
	// the restore brings up.
	f.vmmd.SetDefaultVersion("v1")

	// A live deployment with a published layer but no running instance: the
	// app is parked, exactly as scale-to-zero leaves it.
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID:       target.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("3", 64),
	})
	if err != nil {
		t.Fatalf("create target deployment: %v", err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark target deployment live: %v", err)
	}
	publishNormalPathLayer(t, f, dep.ID)

	if running, err := f.store.RunningInstanceForApp(f.ctx, target.ID); err == nil {
		t.Fatalf("target should start parked, found running instance %q", running.ID)
	}

	status, _, body := pollServiceCall(t, f.h, f.app.ID, "meshparked", "/", http.StatusOK, 30*time.Second)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a parked target must be woken, not 503'd); body=%s", status, body)
	}

	// The wake must have produced a durable instance, not just a lucky proxy.
	if _, err := f.store.RunningInstanceForApp(f.ctx, target.ID); err != nil {
		t.Errorf("no running instance after the service call woke the target: %v", err)
	}
}

// ADR-197: the guest hop must carry the target's configured wire protocol, or
// vmmd routes a gRPC app to the HTTP/1.1 bridge. This is the end-to-end pin for
// the defect that unit tests could not see.
func TestE2E_ServiceMesh_StampsTargetProtocol(t *testing.T) {
	f := newNormalPathFixture(t, "mesh-proto-caller")
	if f == nil {
		return
	}
	target := createServiceApp(t, f, "meshgrpc", map[string]any{"app_protocol": "grpc"})
	_, instance := createNormalPathLiveDeployment(t, f, target.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")

	status, _, body := pollServiceCall(t, f.h, f.app.ID, "meshgrpc", "/rpc", http.StatusOK, 15*time.Second)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	req := f.vmmd.LastRequest()
	if req == nil {
		t.Fatal("fake vmmd received no request")
	}
	if req.AppProtocol != "grpc" {
		t.Errorf("guest hop app_protocol = %q, want grpc (ADR-197: the H2C bridge would never be selected)", req.AppProtocol)
	}
}

// The tenant boundary is the whole security story of the mesh: caller identity
// is established first, then same-account authorization, and an unknown name
// must not disclose whether it exists in someone else's account.
func TestE2E_ServiceMesh_AccessBoundaries(t *testing.T) {
	f := newNormalPathFixture(t, "mesh-authz-caller")
	if f == nil {
		return
	}
	target := createServiceApp(t, f, "meshprivate", nil)
	_, instance := createNormalPathLiveDeployment(t, f, target.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")

	// A second account with its own app: the cross-tenant caller.
	otherKey := f.h.SeedAccount(f.ctx, api.PlanHobby, "mesh-other")
	body, status := doReq(t, f.h, otherKey, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "meshoutsider", Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false)})
	if status != http.StatusCreated {
		t.Fatalf("create outsider app: status=%d body=%s", status, body)
	}
	var outsider api.AppResponse
	if err := json.Unmarshal(body, &outsider); err != nil {
		t.Fatalf("decode outsider app: %v", err)
	}

	tests := []struct {
		name     string
		caller   string
		service  string
		wantCode int
	}{
		{
			name:     "no caller identity",
			caller:   "",
			service:  "meshprivate",
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "cross-account caller is denied",
			caller:   outsider.ID,
			service:  "meshprivate",
			wantCode: http.StatusForbidden,
		},
		{
			name:     "unknown service",
			caller:   f.app.ID,
			service:  "meshghost",
			wantCode: http.StatusNotFound,
		},
		{
			name:     "unknown caller app",
			caller:   "app-does-not-exist",
			service:  "meshprivate",
			wantCode: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, _, body := serviceCall(t, f.h, tc.caller, tc.service, "/")
			if status != tc.wantCode {
				t.Errorf("status = %d, want %d; body=%s", status, tc.wantCode, body)
			}
		})
	}
}
