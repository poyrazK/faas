package servicecaller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func callerKeyServer(t *testing.T, initial TrustedKeys) (*httptest.Server, *atomic.Int64, func(TrustedKeys)) {
	t.Helper()
	var mu sync.RWMutex
	keys := initial
	hits := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		mu.RLock()
		set, err := JWKSFromTrustedKeys(keys)
		mu.RUnlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	update := func(next TrustedKeys) {
		mu.Lock()
		keys = next
		mu.Unlock()
	}
	t.Cleanup(server.Close)
	return server, hits, update
}

func newTestHTTPMiddleware(t *testing.T, endpoint string, required bool, onFailure func(error)) *HTTPMiddleware {
	t.Helper()
	middleware, err := NewHTTPMiddleware(HTTPMiddlewareOptions{
		JWKSURL: endpoint, Audience: targetApp, Require: required, OnFailure: onFailure,
	})
	if err != nil {
		t.Fatalf("NewHTTPMiddleware: %v", err)
	}
	return middleware
}

func assertionRequest(t *testing.T, handler http.Handler, token string, includeHeader bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if includeHeader {
		r.Header.Set(CallerAssertionHeader, token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestHTTPMiddlewareVerifiesAndCachesCaller(t *testing.T) {
	pub, priv := keypair(t)
	kid := KeyID(pub)
	server, hits, _ := callerKeyServer(t, TrustedKeys{kid: pub})
	middleware := newTestHTTPMiddleware(t, server.URL, true, nil)
	token := mintFor(t, priv, kid, defaultInput(), time.Now())

	var calls atomic.Int64
	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get(CallerAssertionHeader); got != "" {
			t.Errorf("assertion header reached application handler: %q", got)
		}
		caller, ok := VerifiedCaller(r.Context())
		if !ok {
			t.Error("VerifiedCaller returned no caller for a valid assertion")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if caller.CallerAppID != callerApp || caller.TargetAppID != targetApp {
			t.Errorf("VerifiedCaller = %+v, want caller %q and target %q", caller, callerApp, targetApp)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	for i := 0; i < 2; i++ {
		if got := assertionRequest(t, handler, token, true).Code; got != http.StatusNoContent {
			t.Fatalf("request %d status = %d, want %d", i, got, http.StatusNoContent)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("next handler calls = %d, want 2", got)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("JWKS fetches = %d, want 1 while the key cache is fresh", got)
	}
}

func TestHTTPMiddlewareRefreshesImmediatelyForUnknownKey(t *testing.T) {
	oldPub, oldPriv := keypair(t)
	newPub, newPriv := keypair(t)
	oldKID, newKID := KeyID(oldPub), KeyID(newPub)
	server, hits, rotate := callerKeyServer(t, TrustedKeys{oldKID: oldPub})
	middleware := newTestHTTPMiddleware(t, server.URL, true, nil)
	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	oldToken := mintFor(t, oldPriv, oldKID, defaultInput(), time.Now())
	if got := assertionRequest(t, handler, oldToken, true).Code; got != http.StatusNoContent {
		t.Fatalf("initial request status = %d, want %d", got, http.StatusNoContent)
	}
	rotate(TrustedKeys{oldKID: oldPub, newKID: newPub})
	newToken := mintFor(t, newPriv, newKID, defaultInput(), time.Now())
	if got := assertionRequest(t, handler, newToken, true).Code; got != http.StatusNoContent {
		t.Fatalf("request with rotated key status = %d, want %d", got, http.StatusNoContent)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("JWKS fetches = %d, want initial fetch plus immediate unknown-kid refresh", got)
	}
}

func TestHTTPMiddlewareRequiredRejectsMissingAndInvalidAssertions(t *testing.T) {
	pub, priv := keypair(t)
	kid := KeyID(pub)
	server, _, _ := callerKeyServer(t, TrustedKeys{kid: pub})
	middleware := newTestHTTPMiddleware(t, server.URL, true, nil)
	var calls atomic.Int64
	handler := middleware.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	wrongAudience := defaultInput()
	wrongAudience.TargetAppID = otherApp
	invalid := mintFor(t, priv, kid, wrongAudience, time.Now())

	for _, tc := range []struct {
		name        string
		token       string
		includeHead bool
	}{
		{name: "missing"},
		{name: "wrong audience", token: invalid, includeHead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := assertionRequest(t, handler, tc.token, tc.includeHead).Code; got != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", got, http.StatusUnauthorized)
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("next handler calls = %d, want 0", got)
	}
}

func TestHTTPMiddlewareOptionalModeDoesNotExposeInvalidCaller(t *testing.T) {
	pub, priv := keypair(t)
	kid := KeyID(pub)
	server, _, _ := callerKeyServer(t, TrustedKeys{kid: pub})
	var failures atomic.Int64
	middleware := newTestHTTPMiddleware(t, server.URL, false, func(error) { failures.Add(1) })
	wrongAudience := defaultInput()
	wrongAudience.TargetAppID = otherApp
	invalid := mintFor(t, priv, kid, wrongAudience, time.Now())

	var calls atomic.Int64
	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get(CallerAssertionHeader); got != "" {
			t.Errorf("unverified assertion header reached application handler: %q", got)
		}
		if caller, ok := VerifiedCaller(r.Context()); ok {
			t.Errorf("invalid assertion exposed as verified caller: %+v", caller)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if got := assertionRequest(t, handler, invalid, true).Code; got != http.StatusNoContent {
		t.Fatalf("optional-mode status = %d, want %d", got, http.StatusNoContent)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("next handler calls = %d, want 1", got)
	}
	if got := failures.Load(); got != 1 {
		t.Errorf("failure callbacks = %d, want 1", got)
	}
}

func TestHTTPMiddlewareRequiredRejectsDuplicateAssertionHeaders(t *testing.T) {
	server, _, _ := callerKeyServer(t, TrustedKeys{})
	middleware := newTestHTTPMiddleware(t, server.URL, true, nil)
	handler := middleware.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler ran with ambiguous assertion headers")
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Add(CallerAssertionHeader, "first.token.value")
	r.Header.Add(CallerAssertionHeader, "second.token.value")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHTTPMiddlewareRequiredRejectsOversizedAssertion(t *testing.T) {
	server, hits, _ := callerKeyServer(t, TrustedKeys{})
	middleware := newTestHTTPMiddleware(t, server.URL, true, nil)
	handler := middleware.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler ran with an oversized assertion")
	}))
	if got := assertionRequest(t, handler, strings.Repeat("a", MaxAssertionBytes+1), true).Code; got != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", got, http.StatusUnauthorized)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("JWKS fetches = %d, want 0 for an oversized token", got)
	}
}

func TestHTTPMiddlewareOptionalMissingShadowsOuterCaller(t *testing.T) {
	server, _, _ := callerKeyServer(t, TrustedKeys{})
	middleware := newTestHTTPMiddleware(t, server.URL, false, nil)
	outer := Assertion{CallerAppID: callerApp, TargetAppID: targetApp}
	ctx := context.WithValue(context.Background(), callerContextKey{}, callerContextValue{
		assertion: outer,
		verified:  true,
	})
	r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if caller, ok := VerifiedCaller(r.Context()); ok {
			t.Errorf("outer caller leaked through absent assertion: %+v", caller)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHTTPMiddlewareRequiredFailsClosedWhenJWKSUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	middleware := newTestHTTPMiddleware(t, server.URL, true, nil)
	_, priv := keypair(t)
	token := mintFor(t, priv, "not-published", defaultInput(), time.Now())
	handler := middleware.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler ran without a trusted key set")
	}))
	if got := assertionRequest(t, handler, token, true).Code; got != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", got, http.StatusUnauthorized)
	}
}

func TestNewHTTPMiddlewareRequiresTrustConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  HTTPMiddlewareOptions
	}{
		{name: "missing audience", cfg: HTTPMiddlewareOptions{JWKSURL: "https://keys.example.test/jwks"}},
		{name: "missing URL", cfg: HTTPMiddlewareOptions{Audience: targetApp}},
		{name: "non-HTTPS URL", cfg: HTTPMiddlewareOptions{Audience: targetApp, JWKSURL: "http://keys.example.test/jwks"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewHTTPMiddleware(tc.cfg); err == nil {
				t.Fatal("NewHTTPMiddleware accepted incomplete or unsafe trust configuration")
			}
		})
	}
}
