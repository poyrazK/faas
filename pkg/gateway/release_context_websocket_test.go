// adr: 259 — browser WebSocket reconnects inherit the document's release graph.
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestIsWebSocketHandshake(t *testing.T) {
	tests := []struct {
		name    string
		upgrade string
		want    bool
	}{
		{name: "websocket", upgrade: "websocket", want: true},
		{name: "case insensitive", upgrade: "WebSocket", want: true},
		{name: "listed protocol", upgrade: "h2c, websocket", want: true},
		{name: "other upgrade", upgrade: "h2c"},
		{name: "missing upgrade", upgrade: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://app.example/socket", nil)
			r.Header.Set("Connection", "Upgrade")
			if tt.upgrade != "" {
				r.Header.Set("Upgrade", tt.upgrade)
			}
			if got := isWebSocketHandshake(r); got != tt.want {
				t.Fatalf("isWebSocketHandshake() = %v, want %v", got, tt.want)
			}
		})
	}
}

func newProjectReleaseCookieHandler(t *testing.T) (*Handler, *fakeBackend, *httptest.Server, string, string, string, string) {
	t.Helper()
	h, backend, upstream := newTestHandler(t)
	oldReleaseID, newReleaseID := uuid.NewString(), uuid.NewString()
	oldDeploymentID, newDeploymentID := uuid.NewString(), uuid.NewString()
	backend.app.ProjectID = uuid.NewString()
	backend.app.RevisionPinTTLSeconds = 3600
	backend.app.WebSocketEnabled = true
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "old", DeploymentID: oldDeploymentID})
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "new", DeploymentID: newDeploymentID})
	h.backend = &releaseBackend{fakeBackend: backend, activeReleaseID: newReleaseID, releaseDeployments: map[string]string{
		oldReleaseID: oldDeploymentID,
		newReleaseID: newDeploymentID,
	}}
	return h, backend, upstream, oldReleaseID, newReleaseID, oldDeploymentID, newDeploymentID
}

func projectWebSocketRequest(cookie string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/socket", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	return r
}

func TestProjectWebSocketUsesReleaseContextCookieAndStripsIt(t *testing.T) {
	h, _, _, oldReleaseID, _, oldDeploymentID, _ := newProjectReleaseCookieHandler(t)
	var selected Target
	var guestCookie, guestRelease string
	h.WithRawForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			selected = target
			guestCookie = r.Header.Get("Cookie")
			guestRelease = r.Header.Get(api.ReleaseHeader)
			w.WriteHeader(http.StatusSwitchingProtocols)
		})
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, projectWebSocketRequest(api.ManagedReleaseContextCookieName+"="+oldReleaseID+"; session=keep"))

	if rec.Code != http.StatusSwitchingProtocols {
		t.Fatalf("websocket status = %d body=%q, want 101", rec.Code, rec.Body.String())
	}
	if selected.DeploymentID != oldDeploymentID || rec.Header().Get(api.RevisionHeader) != oldDeploymentID {
		t.Fatalf("selected deployment = %q response revision=%q, want old deployment %q", selected.DeploymentID, rec.Header().Get(api.RevisionHeader), oldDeploymentID)
	}
	if guestRelease != oldReleaseID || rec.Header().Get(api.ReleaseHeader) != oldReleaseID {
		t.Fatalf("guest release=%q response release=%q, want %q", guestRelease, rec.Header().Get(api.ReleaseHeader), oldReleaseID)
	}
	if guestCookie != "session=keep" {
		t.Fatalf("guest cookie = %q, want only application cookie", guestCookie)
	}
}

func TestProjectWebSocketExplicitReleaseHeaderWinsOverCookie(t *testing.T) {
	h, _, _, oldReleaseID, newReleaseID, _, newDeploymentID := newProjectReleaseCookieHandler(t)
	var selected Target
	h.WithRawForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			selected = target
			w.WriteHeader(http.StatusSwitchingProtocols)
		})
	})
	r := projectWebSocketRequest(api.ManagedReleaseContextCookieName + "=" + oldReleaseID)
	r.Header.Set(api.ReleaseHeader, newReleaseID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusSwitchingProtocols || selected.DeploymentID != newDeploymentID || rec.Header().Get(api.ReleaseHeader) != newReleaseID {
		t.Fatalf("explicit release route = status %d deployment %q release %q, want new graph %q / deployment %q",
			rec.Code, selected.DeploymentID, rec.Header().Get(api.ReleaseHeader), newReleaseID, newDeploymentID)
	}
}

func TestProjectHTTPDoesNotUseWebSocketReleaseContextCookie(t *testing.T) {
	h, _, _, oldReleaseID, newReleaseID, _, newDeploymentID := newProjectReleaseCookieHandler(t)
	var selected Target
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			selected = target
			w.WriteHeader(http.StatusOK)
		})
	})
	r := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/api", nil)
	r.AddCookie(&http.Cookie{Name: api.ManagedReleaseContextCookieName, Value: oldReleaseID})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK || selected.DeploymentID != newDeploymentID || rec.Header().Get(api.ReleaseHeader) != newReleaseID {
		t.Fatalf("ordinary request route = status %d deployment %q release %q, want active graph %q / deployment %q",
			rec.Code, selected.DeploymentID, rec.Header().Get(api.ReleaseHeader), newReleaseID, newDeploymentID)
	}
}

func TestProjectWebSocketReleaseContextCookieFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		cookie     string
		wantStatus int
	}{
		{name: "duplicate", cookie: api.ManagedReleaseContextCookieName + "=" + uuid.NewString() + "; " + api.ManagedReleaseContextCookieName + "=" + uuid.NewString(), wantStatus: http.StatusBadRequest},
		{name: "expired", cookie: api.ManagedReleaseContextCookieName + "=" + uuid.NewString(), wantStatus: http.StatusGone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, _, _, _, _, _ := newProjectReleaseCookieHandler(t)
			var rawCalls int
			h.WithRawForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					rawCalls++
					w.WriteHeader(http.StatusSwitchingProtocols)
				})
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, projectWebSocketRequest(tt.cookie))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d body=%q, want %d", rec.Code, rec.Body.String(), tt.wantStatus)
			}
			if rawCalls != 0 {
				t.Fatalf("raw websocket forwarder called %d times for rejected release cookie", rawCalls)
			}
		})
	}
}
