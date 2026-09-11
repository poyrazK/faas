package gateway

// adr: 120

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

type fakeConsumerAuthStore struct {
	key         ConsumerAuthKey
	consumer    ConsumerAuthConsumer
	keyErr      error
	consumerErr error
	touchCh     chan string
}

func (s *fakeConsumerAuthStore) ConsumerKeyByAppAndPrefix(context.Context, string, string, string) (ConsumerAuthKey, error) {
	return s.key, s.keyErr
}

func (s *fakeConsumerAuthStore) GetAPIConsumerByID(context.Context, string, string) (ConsumerAuthConsumer, error) {
	return s.consumer, s.consumerErr
}

func (s *fakeConsumerAuthStore) TouchConsumerKeyLastUsed(_ context.Context, keyID string) error {
	if s.touchCh != nil {
		s.touchCh <- keyID
	}
	return nil
}

func consumerAuthFixture() (*fakeConsumerAuthStore, string) {
	token := api.ConsumerKeyPrefix + "deadbeef_" + strings.Repeat("ab", 32)
	return &fakeConsumerAuthStore{
		key: ConsumerAuthKey{
			ID:         "key-1",
			AccountID:  "acct-1",
			AppID:      "app-1",
			ConsumerID: "consumer-1",
			Prefix:     "deadbeef",
			Hash:       api.HashConsumerKey(token),
			Scopes:     []string{"read", "write"},
		},
		consumer: ConsumerAuthConsumer{
			ID: "consumer-1", AccountID: "acct-1", AppID: "app-1", Status: "active",
		},
	}, token
}

func runConsumerAuthGate(t *testing.T, h *Handler, app App, method, token string) (*httptest.ResponseRecorder, *http.Request, bool) {
	t.Helper()
	r := httptest.NewRequest(method, "https://app.example.test/resource", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr, status: http.StatusOK, request: r}
	ok := h.enforceConsumerAuth(rr, r, rec, app)
	return rr, r, ok
}

func problemCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var p api.Problem
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v; body=%q", err, rr.Body.String())
	}
	return p.Code
}

func TestEnforceConsumerAuth_OptionalAnonymousPasses(t *testing.T) {
	store, _ := consumerAuthFixture()
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
	rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeOptional}, http.MethodGet, "")
	if !ok || rr.Code != http.StatusOK {
		t.Fatalf("optional anonymous: ok=%v status=%d, want true/200", ok, rr.Code)
	}
}

func TestEnforceConsumerAuth_EmptyModePreservesLegacyAuthorization(t *testing.T) {
	h := NewHandlerWith(nil, nil, nil)
	// Legacy/fake App rows leave ConsumerAuthMode empty while the same
	// request may carry a deployment API key for require_authn/public_auth.
	rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1"}, http.MethodGet,
		"fp_live_"+strings.Repeat("a", 48))
	if !ok || rr.Code != http.StatusOK {
		t.Fatalf("empty consumer auth mode: ok=%v status=%d, want true/200", ok, rr.Code)
	}
}

func TestEnforceConsumerAuth_RequiredAnonymousRejected(t *testing.T) {
	store, _ := consumerAuthFixture()
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
	rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeRequired}, http.MethodGet, "")
	if ok || rr.Code != http.StatusUnauthorized || problemCode(t, rr) != api.CodeConsumerKeyRequired {
		t.Fatalf("required anonymous: ok=%v status=%d code=%s, want false/401/%s", ok, rr.Code, problemCode(t, rr), api.CodeConsumerKeyRequired)
	}
}

func TestEnforceConsumerAuth_ValidKeyStampsBothContexts(t *testing.T) {
	store, token := consumerAuthFixture()
	store.touchCh = make(chan string, 1)
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
	_, r, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeRequired}, http.MethodGet, token)
	if !ok {
		t.Fatal("valid consumer key was rejected")
	}
	identity, ok := authmw.ConsumerFromContext(r)
	if !ok || identity.ID != "consumer-1" || identity.KeyID != "key-1" || identity.AppID != "app-1" {
		t.Fatalf("consumer context = %+v, ok=%v", identity, ok)
	}
	authenticated := authenticatedFrom(r.Context())
	if authenticated.ConsumerID != "consumer-1" || authenticated.ConsumerKeyID != "key-1" {
		t.Fatalf("gateway auth context = %+v", authenticated)
	}
	select {
	case got := <-store.touchCh:
		if got != "key-1" {
			t.Fatalf("touched key %q, want key-1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("valid key did not schedule last-used touch")
	}
}

func TestEnforceConsumerAuth_RejectsInvalidAndInactiveCredentials(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeConsumerAuthStore)
		want   string
	}{
		{name: "invalid format", want: api.CodeConsumerKeyInvalid},
		{name: "revoked", mutate: func(s *fakeConsumerAuthStore) { now := time.Now(); s.key.RevokedAt = &now }, want: api.CodeConsumerKeyInactive},
		{name: "expired", mutate: func(s *fakeConsumerAuthStore) { now := time.Now().Add(-time.Minute); s.key.ExpiresAt = &now }, want: api.CodeConsumerKeyInactive},
		{name: "consumer revoked", mutate: func(s *fakeConsumerAuthStore) { now := time.Now(); s.consumer.RevokedAt = &now }, want: api.CodeConsumerKeyInactive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, token := consumerAuthFixture()
			if tc.mutate != nil {
				tc.mutate(store)
			}
			h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
			if tc.name == "invalid format" {
				token = "not-a-consumer-key"
			}
			rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeOptional}, http.MethodGet, token)
			if ok || rr.Code != http.StatusUnauthorized || problemCode(t, rr) != tc.want {
				t.Fatalf("ok=%v status=%d code=%s, want false/401/%s", ok, rr.Code, problemCode(t, rr), tc.want)
			}
		})
	}
}

func TestEnforceConsumerAuth_ScopeAndTenantChecks(t *testing.T) {
	t.Run("read scope cannot write", func(t *testing.T) {
		store, token := consumerAuthFixture()
		store.key.Scopes = []string{"read"}
		h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
		rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeRequired}, http.MethodPost, token)
		if ok || rr.Code != http.StatusForbidden || problemCode(t, rr) != api.CodeConsumerScopeMissing {
			t.Fatalf("ok=%v status=%d code=%s, want false/403/%s", ok, rr.Code, problemCode(t, rr), api.CodeConsumerScopeMissing)
		}
	})

	t.Run("cross app key is invalid", func(t *testing.T) {
		store, token := consumerAuthFixture()
		store.key.AppID = "other-app"
		h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
		rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeRequired}, http.MethodGet, token)
		if ok || rr.Code != http.StatusUnauthorized || problemCode(t, rr) != api.CodeConsumerKeyInvalid {
			t.Fatalf("ok=%v status=%d code=%s, want false/401/%s", ok, rr.Code, problemCode(t, rr), api.CodeConsumerKeyInvalid)
		}
	})

	t.Run("foreign consumer association is invalid", func(t *testing.T) {
		store, token := consumerAuthFixture()
		store.consumer.AccountID = "other-account"
		h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
		rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeRequired}, http.MethodGet, token)
		if ok || rr.Code != http.StatusUnauthorized || problemCode(t, rr) != api.CodeConsumerKeyInvalid {
			t.Fatalf("ok=%v status=%d code=%s, want false/401/%s", ok, rr.Code, problemCode(t, rr), api.CodeConsumerKeyInvalid)
		}
	})
}

func TestEnforceConsumerAuth_StoreFailureFailsClosed(t *testing.T) {
	store, token := consumerAuthFixture()
	store.keyErr = errors.New("database unavailable")
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
	rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeRequired}, http.MethodGet, token)
	if ok || rr.Code != http.StatusServiceUnavailable || problemCode(t, rr) != api.CodeCapacity {
		t.Fatalf("ok=%v status=%d code=%s, want false/503/%s", ok, rr.Code, problemCode(t, rr), api.CodeCapacity)
	}
}

func TestEnforceConsumerAuth_NotFoundCollapsesToInvalid(t *testing.T) {
	store, token := consumerAuthFixture()
	store.keyErr = ErrConsumerAuthNotFound
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
	rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeOptional}, http.MethodGet, token)
	if ok || rr.Code != http.StatusUnauthorized || problemCode(t, rr) != api.CodeConsumerKeyInvalid {
		t.Fatalf("ok=%v status=%d code=%s, want false/401/%s", ok, rr.Code, problemCode(t, rr), api.CodeConsumerKeyInvalid)
	}
}

func TestEnforceConsumerAuth_UnknownModeFailsClosed(t *testing.T) {
	store, _ := consumerAuthFixture()
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
	rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: "unexpected"}, http.MethodGet, "")
	if ok || rr.Code != http.StatusServiceUnavailable || problemCode(t, rr) != api.CodeCapacity {
		t.Fatalf("ok=%v status=%d code=%s, want false/503/%s", ok, rr.Code, problemCode(t, rr), api.CodeCapacity)
	}
}

func TestConsumerKeyToucher_Debounces(t *testing.T) {
	store := &fakeConsumerAuthStore{touchCh: make(chan string, 2)}
	toucher := newConsumerKeyToucher()
	toucher.touch(store, "key-1")
	select {
	case got := <-store.touchCh:
		if got != "key-1" {
			t.Fatalf("touched key %q, want key-1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first touch was not scheduled")
	}
	toucher.touch(store, "key-1")
	select {
	case got := <-store.touchCh:
		t.Fatalf("second touch %q was not debounced", got)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestConsumerScopeAllows(t *testing.T) {
	tests := []struct {
		name, method string
		scopes       []string
		want         bool
	}{
		{name: "read get", method: http.MethodGet, scopes: []string{"read"}, want: true},
		{name: "read post", method: http.MethodPost, scopes: []string{"read"}, want: false},
		{name: "read head", method: http.MethodHead, scopes: []string{"read"}, want: false},
		{name: "write post", method: http.MethodPost, scopes: []string{"write"}, want: true},
		{name: "write get", method: http.MethodGet, scopes: []string{"write"}, want: true},
		{name: "admin all", method: http.MethodDelete, scopes: []string{"admin"}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := consumerScopeAllows(tc.scopes, tc.method); got != tc.want {
				t.Fatalf("consumerScopeAllows(%v, %s) = %v, want %v", tc.scopes, tc.method, got, tc.want)
			}
		})
	}
}
