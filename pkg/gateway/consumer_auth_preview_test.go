package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

// appScopedConsumerAuthStore answers a key lookup only for the app that owns
// the key, as the durable store does.
type appScopedConsumerAuthStore struct {
	fakeConsumerAuthStore
	lookups []string
}

func (s *appScopedConsumerAuthStore) ConsumerKeyByAppAndPrefix(_ context.Context, _, appID, _ string) (ConsumerAuthKey, error) {
	s.lookups = append(s.lookups, appID)
	if appID != s.key.AppID {
		return ConsumerAuthKey{}, ErrConsumerAuthNotFound
	}
	return s.key, s.keyErr
}

// A preview keeps its production app's consumer-auth gate, but keys were
// looked up by the preview's own app ID, so a required-mode preview refused
// every request (ADR-20261105090226).
func TestEnforceConsumerAuthPreviewAcceptsProductionConsumerKeys(t *testing.T) {
	base, token := consumerAuthFixture()
	store := &appScopedConsumerAuthStore{fakeConsumerAuthStore: *base}
	h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
	preview := App{ID: "preview-1", AccountID: "acct-1", IsPreview: true, ProductionAppID: "app-1", ConsumerAuthMode: api.ConsumerAuthModeRequired}

	_, r, ok := runConsumerAuthGate(t, h, preview, http.MethodGet, token)
	if !ok {
		t.Fatalf("production consumer key refused on its preview (lookups %v)", store.lookups)
	}
	identity, found := authmw.ConsumerFromContext(r)
	if !found || identity.ID != "consumer-1" || identity.AppID != "app-1" {
		t.Fatalf("consumer context = %+v, found=%v", identity, found)
	}
	if got := authenticatedFrom(r.Context()).ConsumerKeyID; got != "key-1" {
		t.Fatalf("authenticated key = %q, want key-1", got)
	}
}

func TestEnforceConsumerAuthProductionFallbackIsPreviewOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  App
	}{
		{"another app", App{ID: "app-2", AccountID: "acct-1", ProductionAppID: "app-1", ConsumerAuthMode: api.ConsumerAuthModeRequired}},
		{"preview of another app", App{ID: "preview-2", AccountID: "acct-1", IsPreview: true, ProductionAppID: "app-2", ConsumerAuthMode: api.ConsumerAuthModeRequired}},
		{"preview without a production app", App{ID: "preview-1", AccountID: "acct-1", IsPreview: true, ConsumerAuthMode: api.ConsumerAuthModeRequired}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, token := consumerAuthFixture()
			store := &appScopedConsumerAuthStore{fakeConsumerAuthStore: *base}
			h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store)
			rr, _, ok := runConsumerAuthGate(t, h, tc.app, http.MethodGet, token)
			if ok || rr.Code != http.StatusUnauthorized {
				t.Fatalf("ok=%v status=%d, want 401", ok, rr.Code)
			}
		})
	}
}

// The pull request's code must not read a production consumer's key.
func TestPreviewWithholdsVerifiedConsumerKey(t *testing.T) {
	for _, tc := range []struct {
		name     string
		preview  bool
		verified bool
		want     string
	}{
		{"preview with a verified key", true, true, ""},
		{"preview with an application credential", true, false, "Bearer app-own"},
		{"production with a verified key", false, true, "Bearer app-own"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "https://pr-1-api.example.test/", nil)
			r.Header.Set("Authorization", "Bearer app-own")
			if tc.verified {
				r = r.WithContext(withAuthenticated(r.Context(), Authenticated{ConsumerID: "consumer-1", ConsumerKeyID: "key-1"}))
			}
			withholdPreviewGateCredential(r, App{ID: "app-1", IsPreview: tc.preview, ConsumerAuthMode: api.ConsumerAuthModeOptional})
			if got := r.Header.Get("Authorization"); got != tc.want {
				t.Fatalf("Authorization = %q, want %q", got, tc.want)
			}
			if withheld := authenticatedFrom(r.Context()).CredentialWithheld; withheld != (tc.want == "") {
				t.Fatalf("CredentialWithheld = %v", withheld)
			}
		})
	}
}
