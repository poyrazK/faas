package apidserver_test

// adr: 20261113160000 — every sign-up records the address, user agent,
// country, network owner and email domain it came from; the record never
// blocks the sign-up.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func signupFactsFixture(t *testing.T, withGeo bool) (*state.MemStore, func(email, addr string) int) {
	t.Helper()
	store := state.NewMemStore()
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	if withGeo {
		srv.SetSignupGeoForTest(
			func(ip net.IP) string {
				if ip.Equal(net.ParseIP("198.51.100.7")) {
					return "NL"
				}
				return ""
			},
			func(net.IP) (uint32, string) { return 64501, "Example Hosting" },
		)
	}
	h := srv.Handler()
	return store, func(email, addr string) int {
		body, _ := json.Marshal(api.PasswordLoginRequest{Email: email, Password: "farm-horse-battery-staple"})
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/signup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "signup-test/1.0")
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
}

func signupFactsFor(t *testing.T, store *state.MemStore, email string) state.AccountSignupFacts {
	t.Helper()
	acct, err := store.AccountByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("AccountByEmail(%s): %v", email, err)
	}
	facts, err := store.AccountSignupFacts(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("AccountSignupFacts(%s): %v", email, err)
	}
	return facts
}

func TestSignupRecordsSignupFacts(t *testing.T) {
	store, signup := signupFactsFixture(t, true)
	if code := signup("new@Mailinator.com", "198.51.100.7:4000"); code >= 300 {
		t.Fatalf("sign-up: %d", code)
	}
	got := signupFactsFor(t, store, "new@mailinator.com")
	if got.Method != "password" || got.SignupIP != "198.51.100.7" || got.UserAgent != "signup-test/1.0" ||
		got.Country != "NL" || got.ASN != 64501 || got.ASNOrg != "Example Hosting" ||
		got.EmailDomain != "mailinator.com" || !got.DisposableEmail {
		t.Fatalf("signup facts = %+v", got)
	}
}

// Without the GeoIP databases (any host but the control plane) the sign-up
// still succeeds and the record keeps what the request itself carries.
func TestSignupFactsWithoutGeoDatabases(t *testing.T) {
	store, signup := signupFactsFixture(t, false)
	if code := signup("person@example.com", "203.0.113.20:4000"); code >= 300 {
		t.Fatalf("sign-up: %d", code)
	}
	got := signupFactsFor(t, store, "person@example.com")
	if got.SignupIP != "203.0.113.20" || got.Country != "" || got.ASN != 0 || got.EmailDomain != "example.com" || got.DisposableEmail {
		t.Fatalf("signup facts without GeoIP = %+v", got)
	}
}
