package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestGoogleAuthRedirect(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "test_google_client_id")
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer(store, log, "example.com", noopNotifier{})

	req := httptest.NewRequest("GET", "/v1/auth/google", nil)
	w := httptest.NewRecorder()

	srv.handler().ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected status 302 Found, got %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "accounts.google.com/o/oauth2/v2/auth") {
		t.Errorf("expected Location header pointing to Google OAuth, got %s", loc)
	}

	cookies := resp.Cookies()
	var foundStateCookie bool
	for _, c := range cookies {
		if c.Name == googleAuthStateCookie {
			foundStateCookie = true
			if c.Value == "" {
				t.Errorf("expected non-empty state cookie value")
			}
		}
	}

	if !foundStateCookie {
		t.Errorf("expected faas_google_state CSRF cookie to be set")
	}
}

func TestGoogleAuthCallbackCSRFMismatch(t *testing.T) {
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer(store, log, "example.com", noopNotifier{})

	req := httptest.NewRequest("GET", "/v1/auth/google/callback?state=invalid_state&code=test_code", nil)
	w := httptest.NewRecorder()

	srv.handler().ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request for CSRF state mismatch, got %d", resp.StatusCode)
	}
}
