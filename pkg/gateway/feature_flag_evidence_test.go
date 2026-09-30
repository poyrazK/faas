package gateway

// adr: 377
// Flag evidence must retain verified customer attribution, stay private to
// Gregale, and preserve separate telemetry cohorts for different decisions.

import (
	"encoding/base64"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFeatureFlagEvidenceGatewayAttribution(t *testing.T) {
	accountID, appID, tenantID, surfaceID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	raw := `[{"flag":"export","value":true,"config_version":12,"rule_id":"selected","reason":"rule_match","source":"configuration","used":true}]`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.PlatformTenantIDHeader) != tenantID {
			t.Errorf("tenant=%q", r.Header.Get(api.PlatformTenantIDHeader))
		}
		w.Header().Set(api.FlagEvidenceHeader, base64.RawURLEncoding.EncodeToString([]byte(raw)))
		w.WriteHeader(503)
	}))
	t.Cleanup(upstream.Close)
	b := &fakeBackend{app: App{ID: appID, AccountID: accountID, Plan: api.PlanPro, ConsumerAuthMode: api.ConsumerAuthModeOptional, RoutedSurfaceID: surfaceID, PlatformTenantID: tenantID}, host: "customer.example", upstream: upstream.Listener.Addr().String()}
	b.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString(), DeploymentID: uuid.NewString()})
	h := NewHandlerWith(b, NewMetrics(), nil)
	h.requestTelemetry = makeTestRecorder()
	r := httptest.NewRequest("GET", "http://customer.example/exports", nil)
	r.Header.Set(api.PlatformTenantIDHeader, "forged")
	r.Header.Set(api.FlagEvidenceHeader, "forged")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get(api.FlagEvidenceHeader) != "" {
		t.Fatalf("response=%d headers=%v", w.Code, w.Header())
	}
	rows := h.requestTelemetry.DrainBatch(1)
	if len(rows) != 1 || rows[0].PlatformTenantID != tenantID || rows[0].FlagEvidenceJSON != raw {
		t.Fatalf("rows=%+v", rows)
	}
}
func TestFlagEvidencePreventsCohortCollapse(t *testing.T) {
	a := RequestTelemetryRow{AccountID: uuid.New(), AppID: uuid.New(), ReceivedAt: time.Now(), LatencyMS: 12, Status: 200, Method: "GET", Route: "/export", FlagEvidenceJSON: `[{"flag":"export","value":true}]`}
	b := a
	b.FlagEvidenceJSON = `[{"flag":"export","value":false}]`
	rows := collapseRequestTelemetry([]RequestTelemetryRow{a, b, a})
	if len(rows) != 2 || rows[0].Count != 2 || rows[1].Count != 1 {
		t.Fatalf("rows=%+v", rows)
	}
}
func TestFlagEvidenceMalformedHeaderAlwaysRemoved(t *testing.T) {
	r := httptestRequestWithGuestEvidence()
	dst := make(http.Header)
	forwardedResponseHeader(r.Context(), dst, api.FlagEvidenceHeader, "invalid")
	if dst.Get(api.FlagEvidenceHeader) != "" {
		t.Fatal(dst)
	}
	resp := &http.Response{Header: http.Header{api.FlagEvidenceHeader: {"invalid"}}, Request: r}
	stripGuestEvidenceResponseHeaders(resp)
	if resp.Header.Get(api.FlagEvidenceHeader) != "" {
		t.Fatal(resp.Header)
	}
}
