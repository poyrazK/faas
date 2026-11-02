package gateway

// adr: 156

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/realtime"
)

// The managed namespace resolves only the host's app: realtimed learns the
// host's account, never the client's claim of one, and nothing wakes or
// admits the app.
func TestManagedRealtimeRouteNamesTheHostAccount(t *testing.T) {
	for _, tt := range []struct {
		name, host, spoofed string
		status              int
		account             string
	}{
		{name: "app host", host: "shop.example.com", status: http.StatusSwitchingProtocols, account: "acct-shop"},
		{name: "client copy replaced", host: "shop.example.com", spoofed: "acct-other", status: http.StatusSwitchingProtocols, account: "acct-shop"},
		{name: "host with no app", host: "ignored.example.com", status: http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeBackend{host: "shop.example.com", app: App{ID: "app-shop", AccountID: "acct-shop"}}
			h := NewHandlerWith(backend, NewMetrics(), nil)
			var got []string
			h.WithManagedRealtime(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != realtime.ManagedPathPrefix+"notifications" {
					t.Errorf("managed path = %q", r.URL.Path)
				}
				got = r.Header.Values(realtime.HostAccountHeader)
				w.WriteHeader(http.StatusSwitchingProtocols)
			}))
			req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+realtime.ManagedPathPrefix+"notifications", nil)
			if tt.spoofed != "" {
				req.Header.Set(realtime.HostAccountHeader, tt.spoofed)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != tt.status {
				t.Fatalf("status = %d, want %d", response.Code, tt.status)
			}
			if tt.account != "" && (len(got) != 1 || got[0] != tt.account) {
				t.Fatalf("%s = %q, want [%s]", realtime.HostAccountHeader, got, tt.account)
			}
			if backend.admits != 0 || backend.pickCalls.Load() != 0 {
				t.Fatalf("the managed namespace admitted (%d) or picked (%d) the app", backend.admits, backend.pickCalls.Load())
			}
		})
	}
}
