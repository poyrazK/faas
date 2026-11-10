package apidserver_test

// adr: 20261104145353 — once the platform created api.SignupAccountsPerDay
// verified accounts in 24 hours, a sign-up from a fresh address is refused
// with 429. Amendment 1: unverified accounts do not count.

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
	"github.com/onebox-faas/faas/pkg/state"
)

func signupFleetCapFixture(t *testing.T) (*state.MemStore, func(email, addr string) int) {
	t.Helper()
	store := state.NewMemStore()
	h := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	return store, func(email, addr string) int {
		body, _ := json.Marshal(api.PasswordLoginRequest{Email: email, Password: "farm-horse-battery-staple"})
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/signup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
}

func seedSignupAccounts(t *testing.T, store *state.MemStore, n int, verified bool) {
	t.Helper()
	for i := range n {
		acct, err := store.CreateAccount(context.Background(), fmt.Sprintf("earlier-%d@example.com", i), api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		if verified {
			if err := store.MarkAccountEmailVerified(context.Background(), acct.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSignupFleetCapRefusesPastTheDailyShare(t *testing.T) {
	store, signup := signupFleetCapFixture(t)
	seedSignupAccounts(t, store, api.SignupAccountsPerDay, true)
	if code := signup("over@example.com", "198.51.100.60:4000"); code != http.StatusTooManyRequests {
		t.Fatalf("sign-up past the platform's daily share of verified accounts from a fresh address: %d, want 429", code)
	}
}

// A signup farm that never verifies claims no Free allowance, so it must not
// close sign-up for everyone else.
func TestSignupFleetCapIgnoresUnverifiedAccounts(t *testing.T) {
	store, signup := signupFleetCapFixture(t)
	seedSignupAccounts(t, store, api.SignupAccountsPerDay, false)
	if code := signup("real@example.com", "203.0.113.50:4000"); code != http.StatusOK {
		t.Fatalf("sign-up after %d unverified accounts: %d, want 200", api.SignupAccountsPerDay, code)
	}
}
