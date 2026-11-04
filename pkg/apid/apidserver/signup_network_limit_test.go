package apidserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The only sign-up throttle was 10 attempts a minute per IP, about 14,000
// accounts a day from one address (bug hunt 2026-10-27 H25). One address
// now creates at most api.SignupAccountsPerAddressPerDay accounts a day.
func TestSignupCapsAccountsPerAddress(t *testing.T) {
	store := state.NewMemStore()
	h := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	signup := func(i int, addr string) int {
		body, _ := json.Marshal(api.PasswordLoginRequest{Email: fmt.Sprintf("farm-%d@example.com", i), Password: "farm-horse-battery-staple"})
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/signup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range api.SignupAccountsPerAddressPerDay {
		if code := signup(i, "203.0.113.7:4000"); code != http.StatusOK {
			t.Fatalf("sign-up %d: %d", i, code)
		}
	}
	if code := signup(100, "203.0.113.7:4000"); code != http.StatusTooManyRequests {
		t.Fatalf("sign-up past the daily cap: %d, want 429", code)
	}
	if code := signup(101, "198.51.100.20:4000"); code != http.StatusOK {
		t.Fatalf("sign-up from another network: %d, want 200", code)
	}
}
