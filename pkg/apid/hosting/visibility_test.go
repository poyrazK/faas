package hosting

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisabledHostingIsIndistinguishableFromMissingRoute(t *testing.T) {
	// adr: 202610171453 — hide the namespace before auth, CORS and method routing.
	h := &testHost{}
	called := 0
	handler := (Service{Host: h}).HideDisabled(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(204)
	}))
	for _, p := range []string{"/v1/internal/hosting", "/v1/internal/hosting/", "/v1/internal/hosting/sites", "/v1/internal/hosting/oidc/exchange", "/v1/internal/hosting/unknown", "/v1/internal/%68osting/sites", "/v1/internal//hosting/sites", "/v1/internal/other/../hosting/sites"} {
		for _, method := range []string{"GET", "POST", "PATCH", "OPTIONS", "HEAD"} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, p, nil)
			r.Header.Set("Origin", "https://customer.example")
			r.Header.Set("Access-Control-Request-Method", "POST")
			handler.ServeHTTP(w, r)
			if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Allow") != "" || strings.Contains(strings.ToLower(w.Body.String()), "hosting") {
				t.Fatalf("%s %s: %d %v %s", method, p, w.Code, w.Header(), w.Body)
			}
		}
	}
	if called != 0 {
		t.Fatal("disabled namespace reached routing")
	}
	for _, p := range []string{"/v1/account", "/v1/internal/hosting-other"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 204 {
			t.Fatal("unrelated route hidden", p)
		}
	}
	h.enabled = true
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1/internal/hosting/oidc/exchange", nil))
	if w.Code != 204 {
		t.Fatal("runtime opt-in ignored")
	}
	h.enabled = false
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/internal/hosting/sites", nil))
	if w.Code != 404 {
		t.Fatal("runtime disable ignored")
	}
}
