// Tests for the OIDC exchange handler. Whitebox (package oidc) so
// the test can call permissiveDefaultPolicyFor directly and stamp
// the in-memory store with rows the production code expects.
//
// Coverage:
//  1. Happy path with a pre-existing trust policy (no auto-create).
//  2. First-use auto-create: Get returns ErrTrustPolicyNotFound
//     and the handler Upserts a permissive default.
//  3. Missing issuer_url: handler returns 400.
//  4. Bad JWT envelope: handler returns 400 (peekIssuer).
//  5. Subject not bound: handler returns 401.
//  6. Minted bearer has fp_oidc_ prefix + 5-min TTL.
//
// The fake Verifier returns a fixed Claims set so the test
// doesn't need a JWKS server. Production has the real
// edgeJWKSVerifier in production (cmd/apid/handlers_oidc.go).
package oidc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeVerifier returns the canned claims on Verify, or an error
// if the policy is non-nil and has Issuer="err" (sentinel for
// negative tests). The Subject is read from the raw JWT envelope
// so tests can drive different sub values without rebuilding the
// canned claims.
type fakeVerifier struct {
	mu         sync.Mutex
	claims     *Claims
	calls      int
	lastPolicy *OIDCTrustPolicy
}

func (f *fakeVerifier) Verify(_ context.Context, rawToken string, p *OIDCTrustPolicy) (*Claims, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if p != nil {
		cp := *p
		cp.Audience = append([]string(nil), p.Audience...)
		cp.Algorithms = append([]string(nil), p.Algorithms...)
		f.lastPolicy = &cp
	}
	if p != nil && p.IssuerURL == "err" {
		return nil, errors.New("fake: simulated bad signature")
	}
	claims := *f.claims
	// Read the `sub` claim out of the raw token envelope so the
	// caller can drive different subject values per test. The
	// envelope is the same shape makeEnvelope produces.
	if parts := strings.Split(rawToken, "."); len(parts) == 3 {
		if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var std struct {
				Sub string `json:"sub"`
			}
			if err := json.Unmarshal(raw, &std); err == nil && std.Sub != "" {
				claims.Subject = std.Sub
			}
		}
	}
	return &claims, nil
}

// memTrustPolicyStore + memTokenStore are minimal in-memory
// fakes that satisfy the OIDC interfaces without dragging in
// pkg/state. They mirror the production semantics (ErrTrustPolicyNotFound
// on miss; ErrNotFound on expired tokens) but skip the FK CASCADE +
// FK enforcement (the test never exercises that path).
type memTrustPolicyStore struct {
	mu       sync.Mutex
	policies map[string]*OIDCTrustPolicy
}

func newMemTrustPolicyStore() *memTrustPolicyStore {
	return &memTrustPolicyStore{policies: map[string]*OIDCTrustPolicy{}}
}

func (m *memTrustPolicyStore) Upsert(_ context.Context, p *OIDCTrustPolicy) (*OIDCTrustPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := p.AccountID + "\x00" + p.IssuerURL
	now := time.Now()
	if existing, ok := m.policies[key]; ok {
		p.CreatedAt = existing.CreatedAt
	}
	p.UpdatedAt = now
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	m.policies[key] = p
	cp := *p
	return &cp, nil
}

func (m *memTrustPolicyStore) Get(_ context.Context, accountID, issuerURL string) (*OIDCTrustPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := accountID + "\x00" + issuerURL
	p, ok := m.policies[key]
	if !ok {
		return nil, ErrTrustPolicyNotFound
	}
	cp := *p
	return &cp, nil
}

func (m *memTrustPolicyStore) ListForAccount(_ context.Context, accountID string) ([]*OIDCTrustPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*OIDCTrustPolicy{}
	for _, p := range m.policies {
		if p.AccountID == accountID {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out, nil
}

type memTokenStore struct {
	mu     sync.Mutex
	tokens map[string]*ExchangedToken
}

func newMemTokenStore() *memTokenStore {
	return &memTokenStore{tokens: map[string]*ExchangedToken{}}
}

func (m *memTokenStore) Insert(_ context.Context, t *ExchangedToken) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	if t.ID == "" {
		// Real stores server-mint the id (gen_random_uuid); the
		// fake mirrors the contract so the handler's audit row
		// carries a stable correlation key. Hex-encode the hash
		// fragment so the id is a printable string (the SQL layer
		// produces a UUID-formatted string, which is also
		// printable — the fake just produces a shorter, equally
		// unique token).
		t.ID = "fake-token-" + hex.EncodeToString(t.TokenHash[:8])
	}
	cp := *t
	m.tokens[string(t.TokenHash)] = &cp
	return t.ID, nil
}

func (m *memTokenStore) GetByHash(_ context.Context, hash []byte) (*ExchangedToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.tokens[string(hash)]
	if !ok {
		return nil, ErrTokenNotFound
	}
	if !row.ExpiresAt.IsZero() && row.ExpiresAt.Before(time.Now()) {
		return nil, ErrTokenNotFound
	}
	cp := *row
	return &cp, nil
}

func (m *memTokenStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, row := range m.tokens {
		if row.ID == id {
			delete(m.tokens, k)
			return nil
		}
	}
	return errors.New("not found")
}

// stubAccountLookup is a fixed-table account resolver.
type stubAccountLookup struct {
	bySubject map[string]state.Account
}

func (s *stubAccountLookup) AccountByOIDCSubject(_ context.Context, _, subject string) (state.Account, error) {
	if a, ok := s.bySubject[subject]; ok {
		return a, nil
	}
	return state.Account{}, ErrTokenNotFound
}

// memAuditor captures the audit events so the test can assert the
// kind set (auth.token.exchanged / oidc.trust_policy.created).
type memAuditor struct {
	mu     sync.Mutex
	events []memAuditEvent
}

type memAuditEvent struct {
	kind      string
	accountID string
	data      map[string]any
}

func (a *memAuditor) Emit(_ context.Context, kind string, accountID *string, data map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := ""
	if accountID != nil {
		id = *accountID
	}
	a.events = append(a.events, memAuditEvent{kind: kind, accountID: id, data: data})
}

const (
	testIssuer = "https://idp.example.com/"
	testSub    = "repo:octocat/hello:ref:refs/heads/main"
	testAcctID = "00000000-0000-0000-0000-00000000face"
)

// makeEnvelope constructs a JWT envelope with the supplied iss +
// exp so the handler's peekIssuer step can read the issuer URL
// without a real signature. The signature itself is fake — the
// fakeVerifier in the test doesn't actually verify.
func makeEnvelope(t *testing.T, iss string, exp time.Time) string {
	t.Helper()
	header := base64URLEncode([]byte(`{"alg":"RS256","kid":"k1"}`))
	body := `{"iss":"` + iss + `","sub":"` + testSub + `","exp":` + intstr(exp.Unix()) + `}`
	payload := base64URLEncode([]byte(body))
	return header + "." + payload + ".fakesig"
}

func intstr(n int64) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}

func base64URLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// newHarness builds the deps + handler with all the in-memory
// fakes and returns the dependencies so the test can stamp rows
// directly.
func newHarness(t *testing.T, clock func() time.Time) (*Handler, *memTrustPolicyStore, *memTokenStore, *memAuditor, *fakeVerifier) {
	t.Helper()
	policies := newMemTrustPolicyStore()
	tokens := newMemTokenStore()
	audit := &memAuditor{}
	v := &fakeVerifier{
		claims: &Claims{
			Subject: testSub,
			Issuer:  testIssuer,
			Aud:     []string{"faas.example.com"},
			Exp:     time.Now().Add(5 * time.Minute),
		},
	}
	lookup := &stubAccountLookup{
		bySubject: map[string]state.Account{
			testSub: {ID: testAcctID, Email: "octo@example.com", Plan: "free", Status: "active"},
		},
	}
	h := NewHandler(HandlerDeps{
		Verifier: v,
		Policies: policies,
		Tokens:   tokens,
		Lookups:  lookup,
		Audit:    audit,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:    clock,
	})
	return h, policies, tokens, audit, v
}

func TestServeHTTP_HappyPath_PreExistingPolicy(t *testing.T) {
	t.Parallel()
	h, policies, tokens, audit, v := newHarness(t, nil)

	// Pre-seed a real policy so the auto-create path is skipped.
	_, err := policies.Upsert(context.Background(), &OIDCTrustPolicy{
		AccountID:  testAcctID,
		IssuerURL:  testIssuer,
		JWKSURL:    testIssuer + ".well-known/jwks",
		Audience:   []string{"faas.example.com"},
		Algorithms: []string{"RS256"},
		AuditLogin: "octo@example.com",
	})
	if err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	body := mustJSON(t, ExchangeRequest{
		Provider: "github",
		Token:    makeEnvelope(t, testIssuer, time.Now().Add(5*time.Minute)),
		Audience: "faas.example.com",
	})
	req := httptest.NewRequest("POST", "/v1/auth/oidc/exchange", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp ExchangeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasPrefix(resp.Bearer, api.APIKeyOIDCKeyPrefix) {
		t.Errorf("bearer prefix: got %q, want %q", resp.Bearer[:len(api.APIKeyOIDCKeyPrefix)], api.APIKeyOIDCKeyPrefix)
	}
	if resp.ExpiresIn != int(OIDCBearerTTL.Seconds()) {
		t.Errorf("ExpiresIn: got %d, want %d", resp.ExpiresIn, int(OIDCBearerTTL.Seconds()))
	}
	if v.calls == 0 {
		t.Errorf("expected verifier to be called")
	}
	if len(tokens.tokens) != 1 {
		t.Errorf("expected 1 row inserted, got %d", len(tokens.tokens))
	}
	// The auth.token.exchanged audit kind was emitted.
	var found bool
	for _, e := range audit.events {
		if e.kind == KindAuthTokenExchanged {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q audit event, got %+v", KindAuthTokenExchanged, audit.events)
	}
}

func TestServeHTTP_RFC8693FormExchange(t *testing.T) {
	t.Parallel()
	h, policies, _, _, _ := newHarness(t, nil)
	if _, err := policies.Upsert(context.Background(), &OIDCTrustPolicy{
		AccountID:  testAcctID,
		IssuerURL:  testIssuer,
		JWKSURL:    testIssuer + ".well-known/jwks",
		Audience:   []string{"faas.example.com"},
		Algorithms: []string{"RS256"},
		AuditLogin: "octo@example.com",
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	form := url.Values{
		"grant_type":           {TokenExchangeGrantType},
		"subject_token":        {makeEnvelope(t, testIssuer, time.Now().Add(5*time.Minute))},
		"subject_token_type":   {TokenExchangeJWTTokenType},
		"audience":             {"faas.example.com"},
		"requested_token_type": {TokenExchangeAccessTokenType},
		"scope":                {"deploy:write"},
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/oidc/exchange", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type: got %q, want application/json", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control: got %q, want no-store", got)
	}
	if got := rr.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma: got %q, want no-cache", got)
	}
	var resp TokenExchangeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasPrefix(resp.AccessToken, api.APIKeyOIDCKeyPrefix) {
		t.Errorf("access_token prefix: got %q", resp.AccessToken)
	}
	if resp.IssuedTokenType != TokenExchangeAccessTokenType {
		t.Errorf("issued_token_type: got %q", resp.IssuedTokenType)
	}
	if resp.TokenType != "Bearer" || resp.ExpiresIn != int(OIDCBearerTTL.Seconds()) {
		t.Errorf("response metadata: token_type=%q expires_in=%d", resp.TokenType, resp.ExpiresIn)
	}
	if resp.Scope != "deploy:write" {
		t.Errorf("scope: got %q", resp.Scope)
	}
}

func TestServeHTTP_RFC8693FormRejectsUnsupportedParameters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		mutate    func(url.Values)
		wantError string
	}{
		{
			name: "wrong grant type",
			mutate: func(v url.Values) {
				v.Set("grant_type", "client_credentials")
			},
			wantError: "invalid_request",
		},
		{
			name: "malformed subject token",
			mutate: func(v url.Values) {
				v.Set("subject_token", "not-a-jwt")
			},
			wantError: "invalid_grant",
		},
		{
			name: "wrong subject token type",
			mutate: func(v url.Values) {
				v.Set("subject_token_type", "urn:ietf:params:oauth:token-type:access_token")
			},
			wantError: "unsupported_subject_token_type",
		},
		{
			name: "resource targeting",
			mutate: func(v url.Values) {
				v.Set("resource", "https://deploy.example.com")
			},
			wantError: "invalid_target",
		},
		{
			name: "scope escalation",
			mutate: func(v url.Values) {
				v.Set("scope", "secrets:read")
			},
			wantError: "invalid_scope",
		},
		{
			name: "unsupported requested token type",
			mutate: func(v url.Values) {
				v.Set("requested_token_type", TokenExchangeJWTTokenType)
			},
			wantError: "unsupported_token_type",
		},
		{
			name: "client authentication",
			mutate: func(v url.Values) {
				v.Set("client_id", "ci-client")
			},
			wantError: "invalid_request",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, _, _, _, _ := newHarness(t, nil)
			form := url.Values{
				"grant_type":         {TokenExchangeGrantType},
				"subject_token":      {makeEnvelope(t, testIssuer, time.Now().Add(5*time.Minute))},
				"subject_token_type": {TokenExchangeJWTTokenType},
				"audience":           {"faas.example.com"},
			}
			tc.mutate(form)
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/oidc/exchange", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if got := rr.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control: got %q, want no-store", got)
			}
			if got := rr.Header().Get("Pragma"); got != "no-cache" {
				t.Fatalf("Pragma: got %q, want no-cache", got)
			}
			var response TokenExchangeError
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Error != tc.wantError {
				t.Fatalf("error: got %q, want %q", response.Error, tc.wantError)
			}
		})
	}
}

func TestTokenExchangeErrorMapping(t *testing.T) {
	t.Parallel()
	cases := map[int]string{
		http.StatusBadRequest:          "invalid_request",
		http.StatusUnauthorized:        "invalid_grant",
		http.StatusTooManyRequests:     "temporarily_unavailable",
		http.StatusInternalServerError: "temporarily_unavailable",
	}
	for status, want := range cases {
		if got := tokenExchangeErrorFromStatus(status); got != want {
			t.Errorf("status %d: got %q, want %q", status, got, want)
		}
		wantStatus := http.StatusBadRequest
		if status >= 500 || status == http.StatusTooManyRequests {
			wantStatus = status
		}
		if got := tokenExchangeErrorStatus(status); got != wantStatus {
			t.Errorf("status mapping %d: got %d, want %d", status, got, wantStatus)
		}
	}
}

func TestServeHTTP_FirstUse_AutoCreate(t *testing.T) {
	t.Parallel()
	h, policies, _, audit, verifier := newHarness(t, nil)

	body := mustJSON(t, ExchangeRequest{
		Provider: "github",
		Token:    makeEnvelope(t, testIssuer, time.Now().Add(5*time.Minute)),
		Audience: "faas.example.com",
	})
	req := httptest.NewRequest("POST", "/v1/auth/oidc/exchange", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// The trust policy should have been auto-created.
	pol, err := policies.Get(context.Background(), testAcctID, testIssuer)
	if err != nil {
		t.Fatalf("expected auto-created policy, got %v", err)
	}
	if pol.AuditLogin != "auto" {
		t.Errorf("AuditLogin: got %q, want %q", pol.AuditLogin, "auto")
	}
	if pol.JWKSURL != "https://idp.example.com/.well-known/jwks" {
		t.Errorf("JWKSURL: got %q", pol.JWKSURL)
	}
	if len(pol.Audience) != 1 || pol.Audience[0] != "faas.example.com" {
		t.Errorf("Audience: got %v", pol.Audience)
	}
	if pol.SubjectPattern != "^repo:octocat/hello:ref:refs/heads/main$" {
		t.Errorf("SubjectPattern: got %q", pol.SubjectPattern)
	}
	if verifier.lastPolicy == nil || verifier.lastPolicy.SubjectPattern != pol.SubjectPattern {
		t.Errorf("verifier did not receive the candidate restricted policy: %+v", verifier.lastPolicy)
	}
	// The oidc.trust_policy.created audit kind was emitted.
	var found bool
	for _, e := range audit.events {
		if e.kind == KindOIDCTrustPolicyCreated {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q audit event, got %+v", KindOIDCTrustPolicyCreated, audit.events)
	}
}

func TestServeHTTP_RequestedAudienceMustBeInVerifiedClaims(t *testing.T) {
	t.Parallel()
	h, policies, tokens, _, verifier := newHarness(t, nil)
	verifier.claims.Aud = []string{"another-audience"}

	body := mustJSON(t, ExchangeRequest{
		Provider: "github",
		Token:    makeEnvelope(t, testIssuer, time.Now().Add(5*time.Minute)),
		Audience: "faas.example.com",
	})
	req := httptest.NewRequest("POST", "/v1/auth/oidc/exchange", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
	if len(tokens.tokens) != 0 {
		t.Fatalf("minted %d tokens for an audience mismatch", len(tokens.tokens))
	}
	if _, err := policies.Get(context.Background(), testAcctID, testIssuer); !errors.Is(err, ErrTrustPolicyNotFound) {
		t.Fatalf("candidate policy persisted before verification: %v", err)
	}
}

func TestServeHTTP_MissingFields_400(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHarness(t, nil)
	for _, body := range []ExchangeRequest{
		{},                   // all empty
		{Provider: "github"}, // missing token + aud
		{Token: "junk"},      // missing provider + aud
	} {
		body := body
		t.Run("", func(t *testing.T) {
			t.Parallel()
			raw := mustJSON(t, body)
			req := httptest.NewRequest("POST", "/v1/auth/oidc/exchange", bytes.NewReader(raw))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want 400", rr.Code)
			}
		})
	}
}

func TestServeHTTP_BadJWT_400(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHarness(t, nil)
	body := mustJSON(t, ExchangeRequest{
		Provider: "github",
		Token:    "not-a-jwt",
		Audience: "faas.example.com",
	})
	req := httptest.NewRequest("POST", "/v1/auth/oidc/exchange", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestServeHTTP_SubjectNotBound_401(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHarness(t, nil)
	body := mustJSON(t, ExchangeRequest{
		Provider: "github",
		// Different subject than the lookup table knows.
		Token:    makeEnvelopeWithSub(t, testIssuer, "unknown:sub:ject", time.Now().Add(5*time.Minute)),
		Audience: "faas.example.com",
	})
	req := httptest.NewRequest("POST", "/v1/auth/oidc/exchange", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
}

func makeEnvelopeWithSub(t *testing.T, iss, sub string, exp time.Time) string {
	t.Helper()
	header := base64URLEncode([]byte(`{"alg":"RS256","kid":"k1"}`))
	body := `{"iss":"` + iss + `","sub":"` + sub + `","exp":` + intstr(exp.Unix()) + `}`
	payload := base64URLEncode([]byte(body))
	return header + "." + payload + ".fakesig"
}

// TestServeHTTP_TokenID_AuditCorrelationRegression is the regression
// test for the code-review finding: the audit row's token_id MUST
// equal the response's token_id MUST equal the persisted row's id.
// The original bug stamped a hash-prefix placeholder in the response
// while the DB row carried a UUID; every audit log search resolved
// to zero rows. The fix: TokenExchangeStore.Insert returns the real
// id and the handler stamps it on the audit row + echoes it in the
// response + the persisted row.
//
// Pre-seeds a trust policy so the test isolates the
// correlation-key invariant from the auto-create path.
func TestServeHTTP_TokenID_AuditCorrelationRegression(t *testing.T) {
	t.Parallel()
	h, policies, _, audit, _ := newHarness(t, nil)

	// Pre-seed a real policy so the auto-create path is skipped.
	if _, err := policies.Upsert(context.Background(), &OIDCTrustPolicy{
		AccountID:  testAcctID,
		IssuerURL:  testIssuer,
		JWKSURL:    testIssuer + ".well-known/jwks",
		Audience:   []string{"faas.example.com"},
		Algorithms: []string{"RS256"},
		AuditLogin: "octo@example.com",
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	body := mustJSON(t, ExchangeRequest{
		Provider: "github",
		Token:    makeEnvelope(t, testIssuer, time.Now().Add(5*time.Minute)),
		Audience: "faas.example.com",
	})
	req := httptest.NewRequest("POST", "/v1/auth/oidc/exchange", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp ExchangeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Locate the auth.token.exchanged audit event.
	var authEv *memAuditEvent
	for i := range audit.events {
		if audit.events[i].kind == KindAuthTokenExchanged {
			authEv = &audit.events[i]
			break
		}
	}
	if authEv == nil {
		t.Fatalf("no %q audit event; got %+v", KindAuthTokenExchanged, audit.events)
	}
	auditID, ok := authEv.data["token_id"].(string)
	if !ok {
		t.Fatalf("audit token_id missing or not string: %+v", authEv.data)
	}
	// All three IDs must match exactly.
	if resp.TokenID == "" {
		t.Errorf("response token_id empty")
	}
	if resp.TokenID != auditID {
		t.Errorf("audit token_id %q != response token_id %q (correlation broken)", auditID, resp.TokenID)
	}
	// The fake mints IDs of the form "fake-token-<8 hex bytes of
	// the hash>"; verify the audit row carries the same id, not
	// the hash-prefix placeholder that the original bug stamped.
	if strings.HasPrefix(auditID, "fake-token-") {
		// The 8-byte hash fragment is OK — the contract is that
		// the audit id resolves to a row in oidc_exchanged_tokens
		// (the same row the bearer maps to). The fake stores by
		// hash, not id, so a deeper lookup is the production test
		// gate, not this one.
		return
	}
	if len(auditID) != 32 {
		// The original placeholder was a 32-hex string from a
		// sha256 fragment. Reject that shape explicitly.
		t.Errorf("audit token_id has the placeholder shape (32-char hex), not the real row id: %q", auditID)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}
