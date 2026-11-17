package middleware

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// adr: 20261117173000. A server-selected setup lifetime must preserve the
// named cookie, account/action binding, and bounded envelope expiry.
func TestCSRFSetupWindowKeepsBindingsAndExpiry(t *testing.T) {
	m := newTestManager(t)
	const name = "faas_csrf_mfa"
	token, err := IssueForAuthenticatedNamedWithTTL(m, "mfa_verify", "account", name, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := m.OpenForCSRF(raw)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(plain, &env); err != nil {
		t.Fatal(err)
	}
	if time.Until(env.Expires) < 59*time.Minute {
		t.Fatal("setup CSRF retained the ordinary ten-minute expiry")
	}
	env.Expires = env.Expires.Add(-20 * time.Minute)
	plain, err = json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	aged, err := m.SealForCSRF(plain)
	if err != nil {
		t.Fatal(err)
	}
	request := func(cookie string) *http.Request {
		return buildPost(t, cookie, aged, FormFieldName+"="+aged)
	}
	if err := VerifyAuthenticatedNamed(m, request(name), "mfa_verify", "account", name); err != nil {
		t.Fatalf("setup form after 20 minutes: %v", err)
	}
	for _, tc := range []struct{ action, account, cookie string }{
		{"delete", "account", name},
		{"mfa_verify", "another-account", name},
		{"mfa_verify", "account", CookieNameAuthenticated},
	} {
		if err := VerifyAuthenticatedNamed(m, request(tc.cookie), tc.action, tc.account, name); err == nil {
			t.Fatal("longer setup form accepted the wrong action, account or cookie")
		}
	}
	env.Expires = time.Now().Add(-time.Second)
	plain, err = json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	aged, err = m.SealForCSRF(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAuthenticatedNamed(m, request(name), "mfa_verify", "account", name); err == nil {
		t.Fatal("setup form survived its bounded expiry")
	}
	for _, ttl := range []time.Duration{0, -time.Second, time.Hour + time.Second} {
		if _, err := IssueForAuthenticatedNamedWithTTL(m, "mfa_verify", "account", name, ttl); err == nil {
			t.Fatalf("setup form accepted invalid lifetime %v", ttl)
		}
	}
}
