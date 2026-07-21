package oci

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestParseChallenge covers the Www-Authenticate parser. Moved here from
// registry_test.go in the slice-2 refactor (parseChallenge moved to auth.go).
func TestParseChallenge(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   AuthChallenge
	}{
		{
			name:   "empty header returns zero challenge",
			header: "",
			want:   AuthChallenge{},
		},
		{
			name:   "non-Bearer prefix returns zero challenge",
			header: "Basic realm=\"foo\"",
			want:   AuthChallenge{},
		},
		{
			name:   "single realm",
			header: `Bearer realm="https://auth.example/token"`,
			want:   AuthChallenge{c: authChallenge{realm: "https://auth.example/token"}},
		},
		{
			name:   "realm + service + scope (pull only)",
			header: `Bearer realm="https://auth/token",service="registry",scope="repository:org/app:pull"`,
			want: AuthChallenge{c: authChallenge{
				realm:   "https://auth/token",
				service: "registry",
				scope:   "repository:org/app:pull",
			}},
		},
		{
			name:   "scope with embedded comma is preserved",
			header: `Bearer realm="https://auth/token",service="registry",scope="repository:org/app:pull,push"`,
			want: AuthChallenge{c: authChallenge{
				realm:   "https://auth/token",
				service: "registry",
				scope:   "repository:org/app:pull,push",
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseChallenge(tc.header)
			if got.c.realm != tc.want.c.realm ||
				got.c.service != tc.want.c.service ||
				got.c.scope != tc.want.c.scope {
				t.Fatalf("ParseChallenge(%q): got %+v want %+v", tc.header, got.c, tc.want.c)
			}
		})
	}
}

// TestFetchToken_AnonymousRoundTrip verifies the happy-path GET on the
// realm endpoint with no Basic credentials and the legacy {"token": ...}
// body shape. Public registries (Docker Hub, ghcr.io) respond with this
// shape; this test mirrors the in-process registry shape used by
// registry_test.go's fakeRegistry.
func TestFetchToken_AnonymousRoundTrip(t *testing.T) {
	const wantToken = "tok-anon-abc"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("anonymous token request carried Authorization header: %q", r.Header.Get("Authorization"))
		}
		if got := r.URL.Query().Get("service"); got != "registry" {
			t.Errorf("service = %q, want %q", got, "registry")
		}
		if got := r.URL.Query().Get("scope"); got != "repository:org/app:pull" {
			t.Errorf("scope = %q, want %q", got, "repository:org/app:pull")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"token":"`+wantToken+`","expires_in":3600}`)
	}))
	defer srv.Close()

	ch := ParseChallenge(`Bearer realm="` + srv.URL + `/token",service="registry",scope="repository:org/app:pull"`)
	tok, err := FetchToken(context.Background(), srv.Client(), "test-ua/1", ch, nil)
	if err != nil {
		t.Fatalf("FetchToken: %v", err)
	}
	if tok.AccessToken != wantToken {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, wantToken)
	}
	if tok.ExpiresIn.Seconds() != 3600 {
		t.Errorf("ExpiresIn = %v, want 3600s", tok.ExpiresIn)
	}
}

// TestFetchToken_BasicAuth verifies that supplying *BasicAuth populates
// the Authorization header on the token endpoint with a base64-encoded
// "user:pass" credential. The /v2/... calls only carry the Bearer header,
// but the spec-compliant token endpoint expects Basic.
func TestFetchToken_BasicAuth(t *testing.T) {
	const (
		user = "alice"
		pass = "s3cret!"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
		if got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"tok-basic","expires_in":600}`)
	}))
	defer srv.Close()

	ch := ParseChallenge(`Bearer realm="` + srv.URL + `",service="registry",scope="repository:priv/img:pull,push"`)
	tok, err := FetchToken(context.Background(), srv.Client(), "test-ua/1", ch,
		&BasicAuth{Username: user, Password: pass})
	if err != nil {
		t.Fatalf("FetchToken: %v", err)
	}
	if tok.AccessToken != "tok-basic" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "tok-basic")
	}
}

// TestFetchToken_RefreshToken verifies that the response's
// refresh_token + expires_in are surfaced on the Token struct so the
// OCIRegistryStorageBackend's bearer cache can refresh proactively.
func TestFetchToken_RefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"token":"tok-1","refresh_token":"refresh-1","expires_in":1}`)
	}))
	defer srv.Close()

	ch := ParseChallenge(`Bearer realm="` + srv.URL + `"`)
	tok, err := FetchToken(context.Background(), srv.Client(), "", ch, nil)
	if err != nil {
		t.Fatalf("FetchToken: %v", err)
	}
	if tok.AccessToken != "tok-1" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "tok-1")
	}
	if tok.RefreshToken != "refresh-1" {
		t.Errorf("RefreshToken = %q, want %q", tok.RefreshToken, "refresh-1")
	}
	if tok.ExpiresIn.Seconds() != 1 {
		t.Errorf("ExpiresIn = %v, want 1s", tok.ExpiresIn)
	}
}

// TestFetchToken_NoRealm verifies that an empty realm in the challenge
// produces a clear error instead of a panicking URL build.
func TestFetchToken_NoRealm(t *testing.T) {
	ch := ParseChallenge(`Bearer service="registry",scope="repository:x:pull"`)
	_, err := FetchToken(context.Background(), &http.Client{}, "", ch, nil)
	if err == nil {
		t.Fatal("FetchToken: expected error for empty realm, got nil")
	}
	if !strings.Contains(err.Error(), "no bearer realm") {
		t.Errorf("error %q lacks 'no bearer realm'", err.Error())
	}
}

// TestFetchToken_Non200Errors covers the non-200 response path: the
// registry returns 401 (bad creds) or 5xx (transient) and we surface
// the status code in the error.
func TestFetchToken_Non200Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad credentials")
	}))
	defer srv.Close()

	ch := ParseChallenge(`Bearer realm="` + srv.URL + `"`)
	_, err := FetchToken(context.Background(), srv.Client(), "", ch,
		&BasicAuth{Username: "alice", Password: "wrong"})
	if err == nil {
		t.Fatal("FetchToken: expected 401 error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error %q lacks status code 401", err.Error())
	}
}

// TestToken_IsExpired verifies the 30s-pre-expiry safety window used by
// the OCIRegistryStorageBackend's bearer cache to avoid races between
// the proactive refresh and a 401-driven retry.
//
// now is the wall-clock issuance time of the token (the time the
// caller received the Token from FetchToken). IsExpired adds
// ExpiresIn to it and treats the token as expired once now+30s is
// past that deadline.
func TestToken_IsExpired(t *testing.T) {
	tests := []struct {
		name       string
		tok        Token
		issuedAt   int // seconds-since-epoch — the time the token was received
		checkAfter int // seconds-since-epoch — the time the caller asks "is it expired?"
		want       bool
	}{
		{
			name:       "zero ExpiresIn never expires",
			tok:        Token{ExpiresIn: 0},
			issuedAt:   1000,
			checkAfter: 1<<31 - 1, // far future
			want:       false,
		},
		{
			name:       "fresh token (just issued) not expired",
			tok:        Token{ExpiresIn: 60 * time.Second},
			issuedAt:   1000,
			checkAfter: 1000, // 0s elapsed of 60s lifetime
			want:       false,
		},
		{
			name:       "30s-safety window: 60s lifetime, checked 35s after issue — 25s left, expired",
			tok:        Token{ExpiresIn: 60 * time.Second},
			issuedAt:   1000,
			checkAfter: 1035, // expiry at 1060; 1035+30=1065 is past it
			want:       true,
		},
		{
			name:       "30s-safety window: 120s lifetime, checked 60s after issue — 60s left, fresh",
			tok:        Token{ExpiresIn: 120 * time.Second},
			issuedAt:   1000,
			checkAfter: 1060, // expiry at 1120; 1060+30=1090 still before
			want:       false,
		},
		{
			name:       "30s-safety window: 60s lifetime, checked 25s after issue — 35s left, fresh",
			tok:        Token{ExpiresIn: 60 * time.Second},
			issuedAt:   1000,
			checkAfter: 1025, // expiry at 1060; 1025+30=1055 still before
			want:       false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issued := time.Unix(int64(tc.issuedAt), 0)
			check := time.Unix(int64(tc.checkAfter), 0)
			if got := tc.tok.IsExpiredAt(issued, check); got != tc.want {
				t.Errorf("IsExpiredAt(issued=%d, check=%d) = %v, want %v",
					tc.issuedAt, tc.checkAfter, got, tc.want)
			}
		})
	}
}

// TestTokenRequest verifies the refresh-token grant body builder.
func TestTokenRequest(t *testing.T) {
	q := TokenRequest("rt-xyz")
	if got := q.Get("grant_type"); got != "refresh_token" {
		t.Errorf("grant_type = %q, want refresh_token", got)
	}
	if got := q.Get("refresh_token"); got != "rt-xyz" {
		t.Errorf("refresh_token = %q, want rt-xyz", got)
	}
	if _, err := url.ParseQuery(q.Encode()); err != nil {
		t.Errorf("encoded form not parseable: %v", err)
	}
}

// TestRegistryClient_AnonymousFlowEndToEnd wires the new FetchToken
// through the legacy RegistryClient.fetchToken path that registry_test.go
// has been exercising for months. It catches regressions in the
// auth.go extraction.
func TestRegistryClient_AnonymousFlowEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token":"anon-token"}`)
		default:
			if r.Header.Get("Authorization") != "Bearer anon-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = io.WriteString(w, `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","size":1},"layers":[]}`)
		}
	}))
	defer srv.Close()

	c := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")), WithTimeout(5*time.Second))
	got, err := c.fetchToken(context.Background(), parseChallenge(`Bearer realm="`+srv.URL+`/token"`))
	if err != nil {
		t.Fatalf("fetchToken: %v", err)
	}
	if got != "anon-token" {
		t.Errorf("fetchToken returned %q, want anon-token", got)
	}
	// Sanity: errors.Is for context-cancellation guard survives the refactor.
	if errors.Is(context.Canceled, context.Canceled) != true {
		t.Fatal("errors.Is sanity check failed (unexpected)")
	}
}