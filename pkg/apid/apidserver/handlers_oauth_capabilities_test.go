package apidserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestRenderAuthCapabilities_NoSession_RedirectsToLogin asserts the
// unauthed path: /v1/auth/capabilities sits behind sessionAuth
// (cmd/apid/server.go) so a scanner without a faas_sid cookie gets
// the 302-to-/login redirect that the rest of the dashboard surface
// emits, NOT a 401 or 200 — the response shape is meant for
// authenticated dashboard sessions only.
func TestRenderAuthCapabilities_NoSession_RedirectsToLogin(t *testing.T) {
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer(store, log, "gregale.dev", noopNotifier{}).handler()

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/capabilities", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect to /login, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/login?next=%2Fv1%2Fauth%2Fcapabilities" {
		t.Errorf("Location = %q, want /login?next=%%2Fv1%%2Fauth%%2Fcapabilities", loc)
	}
}

// TestRenderAuthCapabilities_AllDisabled drives the route through a
// real *server, but bypasses sessionAuth (the unauthed 302 path is
// covered by TestRenderAuthCapabilities_NoSession_RedirectsToLogin
// above). The handler reads s.oauthConfig (zero-value = both
// Disabled) and emits the both-disabled JSON shape.
func TestRenderAuthCapabilities_AllDisabled(t *testing.T) {
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer(store, log, "gregale.dev", noopNotifier{}).WithOAuthConfig(auth.SignInConfig{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/auth/capabilities", nil)
	srv.renderAuthCapabilities(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got api.AuthCapabilities
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body: %v (body=%q)", err, w.Body.String())
	}
	if got.Providers.Google.Enabled || got.Providers.GitHub.Enabled {
		t.Errorf("expected both providers disabled, got %+v", got)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}
}

// TestRenderAuthCapabilities_AllConfigured asserts the both-providers-
// Configured shape (the production state on a fully wired host).
func TestRenderAuthCapabilities_AllConfigured(t *testing.T) {
	cfg := auth.SignInConfig{
		Google: auth.SignInProvider{Status: auth.SignInProviderConfigured, ClientID: "g", ClientSecret: "gs"},
		GitHub: auth.SignInProvider{Status: auth.SignInProviderConfigured, ClientID: "gh", ClientSecret: "ghs"},
	}
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer(store, log, "gregale.dev", noopNotifier{}).WithOAuthConfig(cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/auth/capabilities", nil)
	srv.renderAuthCapabilities(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got api.AuthCapabilities
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body: %v (body=%q)", err, w.Body.String())
	}
	if !got.Providers.Google.Enabled {
		t.Errorf("expected Google enabled, got %+v", got.Providers.Google)
	}
	if !got.Providers.GitHub.Enabled {
		t.Errorf("expected GitHub enabled, got %+v", got.Providers.GitHub)
	}
}

// TestRenderAuthCapabilities_Mixed asserts the half-set shape (one
// provider Configured, the other Disabled). With issue #419 /
// ADR-046's boot validation this case never reaches production —
// a host with only GOOGLE_* set refuses to start. The test still
// covers the runtime guarantee: Enabled() reports false when
// ClientID/ClientSecret are empty, regardless of Status, so the
// capability surface stays consistent.
func TestRenderAuthCapabilities_Mixed(t *testing.T) {
	cfg := auth.SignInConfig{
		Google: auth.SignInProvider{Status: auth.SignInProviderConfigured, ClientID: "g", ClientSecret: "gs"},
		GitHub: auth.SignInProvider{Status: auth.SignInProviderDisabled},
	}
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer(store, log, "gregale.dev", noopNotifier{}).WithOAuthConfig(cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/auth/capabilities", nil)
	srv.renderAuthCapabilities(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got api.AuthCapabilities
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if !got.Providers.Google.Enabled {
		t.Errorf("expected Google enabled, got %+v", got.Providers.Google)
	}
	if got.Providers.GitHub.Enabled {
		t.Errorf("expected GitHub disabled, got %+v", got.Providers.GitHub)
	}
}

// TestAuthCapabilitiesRoute_MountedAndAuthed — end-to-end through
// the full server.handler() mux, exercising the sessionAuth gate on
// /v1/auth/capabilities. The previous three tests bypass sessionAuth
// by calling srv.renderAuthCapabilities directly; this one verifies
// the route is actually mounted under dashboardChain(sessionAuth(...))
// (cmd/apid/server.go:857) and that an authed dashboard session gets
// the JSON shape end-to-end. Issue #419 closes the original 500
// symptom; the wire-shape contract is part of that closure.
func TestAuthCapabilitiesRoute_MountedAndAuthed(t *testing.T) {
	h, cookie := newAuthedDashboardServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/capabilities", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("authed GET /v1/auth/capabilities: code = %d, want 200\nbody = %s",
			rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}
	var got api.AuthCapabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (body=%q)", err, rec.Body.String())
	}
	if got.Providers.Google.Enabled || got.Providers.GitHub.Enabled {
		t.Errorf("zero-value SignInConfig must report both providers disabled, got %+v", got)
	}
}
