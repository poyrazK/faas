package gateway

// adr: 156

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/realtime"
)

func TestManagedRealtimeRoutePrecedesAppLookup(t *testing.T) {
	h := NewHandlerWith(nil, NewMetrics(), nil)
	h.WithManagedRealtime(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != realtime.ManagedPathPrefix+"notifications" {
			t.Errorf("managed path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	req := httptest.NewRequest(http.MethodGet, "http://ignored.example.com"+realtime.ManagedPathPrefix+"notifications", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSwitchingProtocols)
	}
}
