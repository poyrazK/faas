package apidserver_test

// A password alone never yields an API key for an account with a second
// factor, sign-in keys lapse and rotate, a password change revokes them, and
// an API key cannot change the account's sign-in factors.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	signInKeyEmail    = "signin-keys@example.com"
	signInKeyPassword = "signin-horse-battery-staple"
)

type signInKeyEnv struct {
	h     http.Handler
	store *state.MemStore
	acct  state.Account
}

func newSignInKeyEnv(t *testing.T) signInKeyEnv {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, signInKeyEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	phc, err := auth.Encode(signInKeyPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountPassword(ctx, acct.ID, phc); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	return signInKeyEnv{h: srv.Handler(), store: store, acct: acct}
}

func (e signInKeyEnv) signIn(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(api.PasswordLoginRequest{Email: signInKeyEmail, Password: signInKeyPassword})
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e signInKeyEnv) bearer(t *testing.T, method, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e signInKeyEnv) keys(t *testing.T) []state.APIKey {
	t.Helper()
	keys, err := e.store.ListAPIKeys(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func TestPasswordSignInRefusesAccountsWithSecondFactor(t *testing.T) {
	for _, tc := range []struct {
		name string
		arm  func(t *testing.T, e signInKeyEnv)
	}{
		{"enrolled", func(t *testing.T, e signInKeyEnv) {
			if err := e.store.SetMFASecret(context.Background(), e.acct.ID, []byte("sealed-test-secret"), nil); err != nil {
				t.Fatal(err)
			}
			if err := e.store.MarkMFAEnrolled(context.Background(), e.acct.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"required", func(t *testing.T, e signInKeyEnv) {
			if _, err := e.store.SetMFARequired(context.Background(), e.acct.ID, true); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSignInKeyEnv(t)
			tc.arm(t, e)
			for _, path := range []string{"/v1/auth/login", "/v1/auth/signup"} {
				apidserver.AssertProblem(t, e.signIn(t, path), http.StatusForbidden, api.CodeMFARequired)
			}
			if keys := e.keys(t); len(keys) != 0 {
				t.Fatalf("keys after refused sign-ins = %d, want 0", len(keys))
			}
		})
	}
}

func TestPasswordSignInKeysLapseAndRotate(t *testing.T) {
	e := newSignInKeyEnv(t)
	for i := 0; i <= api.SignInAPIKeysMax; i++ {
		if rec := e.signIn(t, "/v1/auth/login"); rec.Code != http.StatusOK {
			t.Fatalf("sign-in %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	live := 0
	for _, k := range e.keys(t) {
		if k.Status == "revoked" {
			continue
		}
		live++
		if k.ExpiresAt == nil {
			t.Fatalf("sign-in key %s never expires", k.ID)
		}
		if left := time.Until(*k.ExpiresAt); left < api.SignInAPIKeyTTL-time.Hour || left > api.SignInAPIKeyTTL {
			t.Fatalf("sign-in key %s lapses in %s, want about %s", k.ID, left, api.SignInAPIKeyTTL)
		}
	}
	if live != api.SignInAPIKeysMax {
		t.Fatalf("live sign-in keys = %d, want %d", live, api.SignInAPIKeysMax)
	}
}

func TestPasswordResetRevokesSignInKeys(t *testing.T) {
	e := newSignInKeyEnv(t)
	rec := e.signIn(t, "/v1/auth/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body)
	}
	var signedIn api.ProgrammaticAuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &signedIn); err != nil {
		t.Fatal(err)
	}
	ciKey, ciHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, ciHash, "ci", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}

	raw := bytes.Repeat([]byte{7}, 32)
	if err := e.store.IssueLoginToken(t.Context(), api.HashToken(raw), e.acct.ID, time.Now().Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"token": {base64.RawURLEncoding.EncodeToString(raw)}, "password": {"a-new-horse-battery-staple"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/reset", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reset := httptest.NewRecorder()
	e.h.ServeHTTP(reset, req)
	if reset.Code != http.StatusFound {
		t.Fatalf("reset: %d %s", reset.Code, reset.Body)
	}

	if rec := e.bearer(t, http.MethodGet, "/v1/account", signedIn.APIKey.Plaintext); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sign-in key after reset: %d, want 401", rec.Code)
	}
	if rec := e.bearer(t, http.MethodGet, "/v1/account", ciKey); rec.Code != http.StatusOK {
		t.Fatalf("customer-created key after reset: %d %s", rec.Code, rec.Body)
	}
}

func TestAPIKeyCannotChangeSignInFactors(t *testing.T) {
	e := newSignInKeyEnv(t)
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "automation", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"mfa_disable", "mfa_confirm", "set_password"} {
		apidserver.AssertProblem(t, e.bearer(t, http.MethodGet, "/v1/auth/csrf?action="+action, key), http.StatusForbidden, api.CodeMFARequired)
	}
	apidserver.AssertProblem(t, e.bearer(t, http.MethodPost, "/v1/account/mfa/enroll", key), http.StatusForbidden, api.CodeMFARequired)
}
