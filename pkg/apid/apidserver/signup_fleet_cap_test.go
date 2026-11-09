package apidserver_test

// adr: 20261104145353 — once the platform created api.SignupAccountsPerDay
// accounts in 24 hours, a sign-up from a fresh address is refused with 429.

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

func TestSignupFleetCapRefusesPastTheDailyShare(t *testing.T) {
	store := state.NewMemStore()
	h := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	signup := func(email, addr string) int {
		body, _ := json.Marshal(api.PasswordLoginRequest{Email: email, Password: "farm-horse-battery-staple"})
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/signup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range api.SignupAccountsPerDay - 1 {
		if _, err := store.CreateAccount(context.Background(), fmt.Sprintf("earlier-%d@example.com", i), api.PlanFree); err != nil {
			t.Fatal(err)
		}
	}
	if code := signup("last@example.com", "203.0.113.50:4000"); code != http.StatusOK {
		t.Fatalf("sign-up within the daily share: %d, want 200", code)
	}
	if code := signup("over@example.com", "198.51.100.60:4000"); code != http.StatusTooManyRequests {
		t.Fatalf("sign-up past the platform's daily share from a fresh address: %d, want 429", code)
	}
}
