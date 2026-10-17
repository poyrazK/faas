package apidserver_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlEmailRoutesAndGates(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	s := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	paths := []string{"/v1/product-control/portal-email.js", "/v1/product-control/customer/product/email-preferences", "/v1/product-control/customer/product/email-deliveries"}
	for _, path := range paths {
		for _, method := range []string{"GET", "HEAD", "PUT", "POST", "PATCH", "DELETE", "OPTIONS"} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, "https://example.test"+path, nil)
			r.Header.Set("Authorization", "Bearer owner-must-not-grant-customer-access")
			s.Handler().ServeHTTP(w, r)
			if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("disabled %s %s: %d", method, path, w.Code)
			}
		}
	}
	s.WithProductControlEnabled(true)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://example.test"+paths[0], nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "GregaleProductEmail") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("email assets not routed under CSP")
	}
	for _, path := range paths[1:] {
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "https://example.test"+path, nil))
		if w.Code != 405 {
			t.Fatalf("unregistered email method %s: %d", path, w.Code)
		}
	}
}

func TestProductControlEmailConfigurationDefaultsAndSeparateSendSwitch(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	for _, tc := range []struct {
		name, mode, send   string
		enabled, wantError bool
	}{
		{"default", "", "", true, false}, {"disabled feature", "resend", "", false, false}, {"local", "local", "", true, false}, {"real send switch required", "resend", "", true, true}, {"real credentials required", "resend", "1", true, true}, {"noop rejected", "noop", "", true, true}, {"log rejected", "log", "", true, true}, {"postmark rejected", "postmark", "1", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
			s.WithProductControlEnabled(tc.enabled)
			env := map[string]string{"FAAS_PRODUCT_CONTROL_EMAIL_MODE": tc.mode, "FAAS_PRODUCT_CONTROL_EMAIL_SEND_ENABLED": tc.send, "FAAS_PRODUCT_CONTROL_EMAIL_SINK_DIR": filepath.Join(t.TempDir(), "sink")}
			err := s.ConfigureProductEmail(func(k string) string { return env[k] })
			if (err != nil) != tc.wantError {
				t.Fatal(err)
			}
			if !tc.enabled || tc.mode == "" {
				if s.ProductControl().EmailTransport != nil {
					t.Fatal("sending enabled by default")
				}
			}
		})
	}
}
