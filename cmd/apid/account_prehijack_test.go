package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	magicLinkTokenPattern   = regexp.MustCompile(`/auth/verify\?token=([A-Za-z0-9_-]+)`)
	verifyEmailTokenPattern = regexp.MustCompile(`/v1/auth/verify-email\?token=([A-Za-z0-9_-]+)`)
)

func lastMailToken(t *testing.T, mailer *recordingMailer, pattern *regexp.Regexp) string {
	t.Helper()
	msgs := mailer.snapshot()
	for i := len(msgs) - 1; i >= 0; i-- {
		if m := pattern.FindStringSubmatch(msgs[i].TextBody); m != nil {
			return m[1]
		}
	}
	t.Fatalf("no mail matched %s", pattern)
	return ""
}

func signupWithPassword(t *testing.T, h http.Handler, email, password string) string {
	t.Helper()
	rec := v1AuthJSONRequest(t, h, "/v1/auth/signup", `{"email":"`+email+`","password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("signup = %d: %s", rec.Code, rec.Body.String())
	}
	var resp api.ProgrammaticAuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.APIKey.Plaintext == "" {
		t.Fatalf("signup response %s: %v", rec.Body.String(), err)
	}
	return resp.APIKey.Plaintext
}

func bearerStatus(h http.Handler, key string) int {
	req := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// The magic-link mailer encodes tokens base64url but /auth/verify decoded
// only hex, so every magic link 410'd.
func TestMagicLinkFromSignupEndpointSignsIn(t *testing.T) {
	srv, mailer, _ := v1AuthTestHarness(t)
	h := srv.handler()
	if rec := v1AuthJSONRequest(t, h, "/v1/auth/signup/magic-link", `{"email":"magic-works@example.com"}`); rec.Code != http.StatusOK {
		t.Fatalf("magic-link request = %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/verify?token="+lastMailToken(t, mailer, magicLinkTokenPattern), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Set-Cookie") == "" {
		t.Fatalf("magic link = %d (Set-Cookie %q), want a signed-in redirect: %s", rec.Code, rec.Header().Get("Set-Cookie"), rec.Body.String())
	}
}

// Account pre-hijacking: an attacker signs up with the victim's address and
// a password (unverified, but the signup still mints an API key). When the
// real owner later proves the address through a magic link or an OAuth
// provider, everything minted before that proof must stop working.
func TestEmailOwnershipClaimRevokesPreVerificationCredentials(t *testing.T) {
	srv, mailer, store := v1AuthTestHarness(t)
	h := srv.handler()
	attackerKey := signupWithPassword(t, h, "victim@example.com", "attacker-password-123!")
	if code := bearerStatus(h, attackerKey); code != http.StatusOK {
		t.Fatalf("attacker key before claim = %d, want 200", code)
	}

	if rec := v1AuthJSONRequest(t, h, "/v1/auth/signup/magic-link", `{"email":"victim@example.com"}`); rec.Code != http.StatusOK {
		t.Fatalf("magic-link request = %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/verify?token="+lastMailToken(t, mailer, magicLinkTokenPattern), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("owner's magic link = %d: %s", rec.Code, rec.Body.String())
	}

	if code := bearerStatus(h, attackerKey); code != http.StatusUnauthorized {
		t.Errorf("attacker signup key after the owner claimed the address = %d, want 401", code)
	}
	if rec := v1AuthJSONRequest(t, h, "/v1/auth/login", `{"email":"victim@example.com","password":"attacker-password-123!"}`); rec.Code == http.StatusOK {
		t.Errorf("attacker password still signs in after the owner claimed the address")
	}
	acct, err := store.AccountByEmail(context.Background(), "victim@example.com")
	if err != nil || !acct.EmailVerified() {
		t.Fatalf("account after claim verified=%v err=%v", acct.EmailVerified(), err)
	}

	// OAuth is the other claim channel; it shares the helper.
	srv2, _, store2 := v1AuthTestHarness(t)
	h2 := srv2.handler()
	key2 := signupWithPassword(t, h2, "oauth-victim@example.com", "attacker-password-123!")
	acct2, err := store2.AccountByEmail(context.Background(), "oauth-victim@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv2.verifyOAuthAccountEmail(context.Background(), acct2); err != nil {
		t.Fatal(err)
	}
	if code := bearerStatus(h2, key2); code != http.StatusUnauthorized {
		t.Errorf("attacker signup key after an OAuth claim = %d, want 401", code)
	}
}

// The verify-email link goes to the address that set the password, so it
// proves the password creator owns the address: credentials survive it.
func TestVerifyEmailLinkKeepsSignupCredentials(t *testing.T) {
	srv, mailer, _ := v1AuthTestHarness(t)
	h := srv.handler()
	key := signupWithPassword(t, h, "owner@example.com", "owner-password-123!")
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/verify-email?token="+lastMailToken(t, mailer, verifyEmailTokenPattern), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		t.Fatalf("verify-email = %d: %s", rec.Code, rec.Body.String())
	}
	if code := bearerStatus(h, key); code != http.StatusOK {
		t.Errorf("signup key after the owner's own verify-email link = %d, want 200", code)
	}
	if rec := v1AuthJSONRequest(t, h, "/v1/auth/login", `{"email":"owner@example.com","password":"owner-password-123!"}`); rec.Code != http.StatusOK {
		t.Errorf("owner password after verify-email = %d, want 200", rec.Code)
	}
}
