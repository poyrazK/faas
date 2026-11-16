package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/state"
)

// Failures from one client network pause only that network: the owner
// signing in from another network is not held back (F10-1,
// ADR-20261115100000). The fleet-wide budget (LoginFailuresPerEmailFleet)
// still bounds guessing spread over many networks.
func TestPasswordSignInPauseIsPerNetwork(t *testing.T) {
	const email, password = "per-network@example.com", "right-horse-battery-staple"
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	phc, err := auth.Encode(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountPassword(ctx, acct.ID, phc); err != nil {
		t.Fatal(err)
	}
	h := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	signIn := func(remote, password string) int {
		body, _ := json.Marshal(api.PasswordLoginRequest{Email: email, Password: password})
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range api.LoginFailuresPerEmail {
		if code := signIn(fmt.Sprintf("198.51.100.%d:4321", i+1), fmt.Sprintf("wrong-%d", i)); code != http.StatusUnauthorized {
			t.Fatalf("guess %d: status %d, want 401", i, code)
		}
	}
	if code := signIn("198.51.100.250:4321", password); code != http.StatusTooManyRequests {
		t.Fatalf("attacker's network after %d failures: status %d, want 429", api.LoginFailuresPerEmail, code)
	}
	if code := signIn("203.0.113.7:4321", password); code != http.StatusOK {
		t.Fatalf("owner on another network: status %d, want 200", code)
	}
}

func TestLoginNetwork(t *testing.T) {
	for ip, want := range map[string]string{
		"198.51.100.7":        "198.51.100.0/24",
		"::ffff:198.51.100.7": "198.51.100.0/24",
		"2001:db8:1:2::5":     "2001:db8:1::/48",
		"not-an-address":      "unknown",
		"":                    "unknown",
	} {
		if got := apidserver.LoginNetwork(ip); got != want {
			t.Errorf("LoginNetwork(%q) = %q, want %q", ip, got, want)
		}
	}
}
