package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The edge never forwards gatewayd-internal's private paths: realtimed's
// management API answered any app host's /v1/internal/realtime/internal/...
func TestRefuseComputePrivatePaths(t *testing.T) {
	for _, tc := range []struct {
		target  string
		refused bool
	}{
		{"https://victim.gregale.dev/v1/internal/realtime/internal/connections", true},
		{"https://victim.gregale.dev/v1/internal/realtime/internal/endpoints/ep_1/channels/c:publish", true},
		{"https://shop.example.com/v1/internal/realtime", true},
		{"https://api.gregale.dev/v1/internal/realtime/internal/stats", true},
		{"https://victim.gregale.dev//v1/internal/realtime/internal/connections", true},
		{"https://victim.gregale.dev/v1/internal/./realtime/internal/connections", true},
		{"https://victim.gregale.dev/v1/apps/../internal/realtime/internal/connections", true},
		{"https://victim.gregale.dev/v1/internal/realtime%2Finternal%2Fconnections", true},
		{"https://victim.gregale.dev/v1/internal/metrics", true},
		{"https://victim.gregale.dev/v1/internal/realtimes", false},
		{"https://victim.gregale.dev/v1/internal/services/payments/charge", false},
		{"https://victim.gregale.dev/v1/orders", false},
		{"https://victim.gregale.dev/", false},
	} {
		forwarded := false
		h := refuseComputePrivatePaths(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwarded = true
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))
		if tc.refused && (forwarded || rec.Code != http.StatusNotFound) {
			t.Errorf("%s: forwarded=%v status=%d, want 404 without forwarding", tc.target, forwarded, rec.Code)
		}
		if !tc.refused && (!forwarded || rec.Code != http.StatusOK) {
			t.Errorf("%s: forwarded=%v status=%d, want it forwarded", tc.target, forwarded, rec.Code)
		}
	}
}

// Both the control-plane proxy and the compute edge sit under rootHandler,
// so the guard must wrap rootHandler itself.
func TestRootHandlerRefusesComputePrivatePaths(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	guard := strings.Index(body, "rootHandler = refuseComputePrivatePaths(rootHandler)")
	mount := strings.Index(body, `traceMux.Handle("/", rootHandler)`)
	edge := strings.Index(body, "rootHandler, err = computeEdgeRoot(")
	if guard < 0 || mount < 0 || edge < 0 || !(edge < guard && guard < mount) {
		t.Fatal("main.go must wrap rootHandler with refuseComputePrivatePaths after the compute edge is chosen and before it is mounted")
	}
}
