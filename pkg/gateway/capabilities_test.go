// adr: 20261023115741 — the request path stops a capability's behavior while
// its platform flag is off for the app's account.
package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// capabilitiesOff decides the named capabilities off for every account.
func capabilitiesOff(off ...string) func(capability, accountID string) bool {
	return func(capability, _ string) bool {
		for _, id := range off {
			if id == capability {
				return false
			}
		}
		return true
	}
}

func TestWakePageOffHoldsTheBrowserRequestForTheWake(t *testing.T) {
	base, backend, _ := newTestHandler(t)
	pageBackend := &pageWakeBackend{fakeBackend: backend, delay: time.Duration(api.WakePageAfterMs+200) * time.Millisecond}
	h := NewHandlerWith(pageBackend, NewMetrics(), base.log).WithCapabilities(capabilitiesOff(CapabilityWakePage))

	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(wakePageHeader); got != "" {
		t.Fatalf("%s = %q with wake-page off, want no wake page", wakePageHeader, got)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "hello from app" {
		t.Fatalf("response = %d %q, want the app's answer after the wake", rec.Code, rec.Body.String())
	}
}

func TestSessionAffinityOffIgnoresTheCookie(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	h.WithCapabilities(capabilitiesOff(CapabilitySessionAffinity))
	backend.mu.Lock()
	backend.app.SessionAffinity = true
	appID := backend.app.ID
	backend.mu.Unlock()
	value, err := h.sessionAffinityCookieValue(appID, "i-frozen")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	req.AddCookie(&http.Cookie{Name: sessionAffinityCookieName, Value: value})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	backend.mu.Lock()
	got := backend.lastAdmitPreferred
	backend.mu.Unlock()
	if got != "" {
		t.Fatalf("wake hint = %q with session-affinity off, want none", got)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionAffinityCookieName {
			t.Fatalf("affinity cookie set with session-affinity off")
		}
	}
}

func TestTrafficMirroringOffSendsNoShadowTraffic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("source response"))
	}))
	t.Cleanup(upstream.Close)
	b := &mirrorFakeBackend{
		app: App{
			ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro,
			PublicAuth: PublicAuthConfig{Mode: api.AppPublicAuthModeOpen},
		},
		host:         "jane-api.apps.dom",
		upstreamAddr: upstream.Listener.Addr().String(),
		mirrorRules:  []MirrorRuleRow{{ID: "rule-1", AppID: "app-1", MirrorDeploymentID: "dep-B", Percent: 100}},
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithMirrorRoundTripper(&stubMirrorRoundTripper{})
	h.WithCapabilities(capabilitiesOff(CapabilityTrafficMirroring))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	if got := b.scheduleCalls.Load(); got != 0 {
		t.Fatalf("ScheduleMirror calls = %d with traffic-mirroring off, want 0", got)
	}
}
