package hosting

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgejwks"
	"github.com/onebox-faas/faas/pkg/oidc"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *testHost) HostingOIDCEnabled() bool { return h.oidc }
func (h *testHost) HostingOIDCStore() state.HostingOIDCStore {
	if h.oidcStore != nil {
		return h.oidcStore
	}
	return h.store
}
func (h *testHost) HostingOIDCAccount(ctx context.Context, id string) (state.Account, error) {
	return h.store.AccountByID(ctx, id)
}

type localGitHubVerifier struct {
	t        *testing.T
	verifier edgejwks.Verifier
	url      string
	after    func()
}

func (v *localGitHubVerifier) Verify(ctx context.Context, token string, p *oidc.OIDCTrustPolicy) (*oidc.Claims, error) {
	if p.IssuerURL != "https://token.actions.githubusercontent.com" || p.JWKSURL != "https://token.actions.githubusercontent.com/.well-known/jwks" || len(p.Algorithms) != 1 || p.Algorithms[0] != "RS256" {
		v.t.Fatal("production verifier endpoint or algorithm changed", p)
	}
	c, e := v.verifier.Verify(ctx, token, edgejwks.VerifierRule{JWKSURL: v.url, Issuer: p.IssuerURL, Audience: p.Audience, Algorithms: p.Algorithms, RequiredClaims: p.RequiredClaims})
	if e != nil {
		return nil, e
	}
	if v.after != nil {
		v.after()
	}
	return &oidc.Claims{Subject: c.Subject, Issuer: c.Issuer, Aud: c.Aud, Exp: c.Exp}, nil
}

func TestHostingOIDCSignedIdentityAndRevocationDuringExchange(t *testing.T) {
	// adr: 202610171028 — signed immutable claims are authorization; source labels never substitute for them.
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	h := &testHost{enabled: true, allowed: true, durable: true, automation: true, oidc: true, store: store, artifacts: &testArtifacts{}}
	trust, err := store.CreateHostingOIDCTrust(t.Context(), api.HostingOIDCTrust{ID: uuid.NewString(), AccountID: account.ID, SiteID: site.ID, CreatedAt: time.Now().UTC(), HostingOIDCTrustRules: api.HostingOIDCTrustRules{RepositoryOwnerID: "123", RepositoryID: "456", Repository: "org/site", Ref: "refs/heads/main", WorkflowRef: "org/site/.github/workflows/preview.yml@refs/heads/main", EventName: "push", Environment: "pilot"}})
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "github", Algorithm: "RS256", Use: "sig"}}})
	}))
	defer jwks.Close()
	cache := edgejwks.NewCache(edgejwks.Options{HTTPClient: jwks.Client()})
	if err = cache.Register(jwks.URL); err != nil {
		t.Fatal(err)
	}
	verifier := &localGitHubVerifier{t: t, verifier: edgejwks.NewVerifier(cache, edgejwks.DefaultSkew), url: jwks.URL}
	s := Service{Host: h, OIDCVerifier: verifier}
	claims := func() map[string]any {
		return map[string]any{"iss": "https://token.actions.githubusercontent.com", "sub": "repo:org/site:environment:pilot", "aud": api.HostingOIDCAudience(site.ID), "exp": time.Now().Add(4 * time.Minute).Unix(), "iat": time.Now().Unix(), "repository_owner_id": "123", "repository_id": "456", "repository": "org/site", "ref": "refs/heads/main", "workflow_ref": trust.WorkflowRef, "event_name": "push", "environment": "pilot"}
	}
	sign := func(values map[string]any, private *rsa.PrivateKey) string {
		signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: private}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "github"))
		if e != nil {
			t.Fatal(e)
		}
		raw, e := jwt.Signed(signer).Claims(values).Serialize()
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	exchange := func(token string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(api.HostingOIDCExchangeRequest{SiteID: site.ID, TrustID: trust.ID, Assertion: token})
		r := httptest.NewRequest("POST", "/v1/internal/hosting/oidc/exchange", strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		s.ExchangeOIDC(w, r)
		return w
	}
	for field, values := range map[string][]any{"iss": {"https://evil.example"}, "aud": {api.HostingOIDCAudience(uuid.NewString())}, "repository_owner_id": {"999", 123}, "repository_id": {"999", 456}, "repository": {"fork/site"}, "ref": {"refs/heads/other", "refs/pull/1/merge"}, "workflow_ref": {"org/site/.github/workflows/untrusted.yml@refs/heads/main"}, "event_name": {"pull_request", "pull_request_target"}, "environment": {"production", nil}, "exp": {time.Now().Add(-time.Second).Unix(), time.Now().Add(time.Hour).Unix(), nil}, "sub": {""}} {
		for _, value := range values {
			t.Run(field+"/"+stringJSON(value), func(t *testing.T) {
				c := claims()
				if value == nil {
					delete(c, field)
				} else {
					c[field] = value
				}
				w := exchange(sign(c, key))
				if w.Code != 401 || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal(w.Code, w.Body)
				}
			})
		}
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if w := exchange(sign(claims(), other)); w.Code != 401 {
		t.Fatal("bad signature", w.Code)
	}
	token := sign(claims(), key)
	for _, disable := range []func(){func() { h.oidc = false }, func() { h.automation = false }, func() { h.enabled = false }, func() { h.durable = false }} {
		disable()
		w := exchange(token)
		if w.Code != 503 {
			t.Fatal("gate", w.Code, w.Body)
		}
		h.oidc = true
		h.automation = true
		h.enabled = true
		h.durable = true
	}
	h.allowed = false
	if w := exchange(token); w.Code != 403 {
		t.Fatal("operator allowlist", w.Code)
	}
	h.allowed = true
	w := exchange(token)
	var response api.HostingAutomationCredentialResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || !api.ValidHostingAutomationToken(response.Token) || response.Credential.OIDCTrustID != trust.ID || response.Credential.ExpiresAt.After(time.Now().Add(api.HostingOIDCBearerTTL)) {
		t.Fatal(w.Code, w.Body)
	}
	if _, _, err = store.AuthenticateHostingAutomationToken(t.Context(), api.HashAPIKey(response.Token)); err != nil {
		t.Fatal(err)
	}
	verifier.after = func() {
		if e := store.RevokeHostingOIDCTrust(t.Context(), account.ID, site.ID, trust.ID); e != nil {
			t.Fatal(e)
		}
	}
	if w = exchange(token); w.Code == 200 {
		t.Fatal("revocation during JWKS verification minted session")
	}
	if _, _, err = store.AuthenticateHostingAutomationToken(t.Context(), api.HashAPIKey(response.Token)); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("issued bearer survived trust revocation", err)
	}
}
func stringJSON(value any) string { data, _ := json.Marshal(value); return string(data) }
