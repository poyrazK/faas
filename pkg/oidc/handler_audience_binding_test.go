package oidc

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// The existing exchange tests name "faas.example.com"; a policy Gregale
// creates now accepts only Gregale's audience or a configured extra (H68).
func TestMain(m *testing.M) {
	_ = os.Setenv("FAAS_OIDC_EXCHANGE_AUDIENCES", "faas.example.com")
	os.Exit(m.Run())
}

type repositoryIDLookup struct {
	bindingAwareLookup
	ownerID, repoID int64
}

func (l *repositoryIDLookup) GitHubRepositoryIDsForOIDCSubject(context.Context, string, string, string) (int64, int64, error) {
	return l.ownerID, l.repoID, nil
}

func exchangeOnce(t *testing.T, h *Handler, subject, aud string) *httptest.ResponseRecorder {
	t.Helper()
	body := mustJSON(t, ExchangeRequest{
		Provider: "github",
		Token:    makeEnvelopeWithSub(t, testIssuer, subject, time.Now().Add(5*time.Minute)),
		Audience: aud,
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/v1/auth/oidc/exchange", bytes.NewReader(body)))
	return rr
}

// A GitHub token minted for another relying party (Sigstore, PyPI, AWS)
// created or satisfied a policy Gregale made on first use, which pinned
// whatever audience the replayer named (bug hunt 2026-10-28 H68).
func TestServeHTTP_FirstUseNeedsGregaleAudience(t *testing.T) {
	t.Setenv("FAAS_OIDC_EXCHANGE_AUDIENCES", "")
	for _, tc := range []struct {
		aud  string
		want int
	}{
		{"sigstore", http.StatusUnauthorized},
		{"sts.amazonaws.com", http.StatusUnauthorized},
		{DefaultAudience, http.StatusOK},
	} {
		t.Run(tc.aud, func(t *testing.T) {
			h, policies, _, _, v := newHarness(t, time.Now)
			v.claims.Aud = []string{tc.aud}
			if rr := exchangeOnce(t, h, testSub, tc.aud); rr.Code != tc.want {
				t.Fatalf("first exchange with aud %q: %d %s, want %d", tc.aud, rr.Code, rr.Body, tc.want)
			}
			if _, err := policies.Get(context.Background(), testAcctID, testIssuer); (err == nil) != (tc.want == http.StatusOK) {
				t.Fatalf("policy stored = %v after aud %q", err == nil, tc.aud)
			}
		})
	}
}

// A trust policy the customer wrote is authoritative: the repository
// binding no longer admits other branches or workflows past it (H46).
func TestServeHTTP_ExplicitPolicyIsNotWidenedByBinding(t *testing.T) {
	account := state.Account{ID: testAcctID, Email: "octo@example.com", Plan: "free", Status: "active"}
	other := "repo:octocat/hello:ref:refs/heads/feature-7"
	policies := newMemTrustPolicyStore()
	pinned := "^" + regexp.QuoteMeta(testSub) + "$"
	if _, err := policies.Upsert(context.Background(), &OIDCTrustPolicy{
		AccountID: testAcctID, IssuerURL: testIssuer, JWKSURL: testIssuer + ".well-known/jwks",
		Audience: []string{"faas.example.com"}, SubjectPattern: pinned,
		Algorithms: []string{"RS256"}, AuditLogin: "octo@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	lookup := &bindingAwareLookup{
		stubAccountLookup: stubAccountLookup{bySubject: map[string]state.Account{testSub: account, other: account}},
		bound:             map[string]state.Account{other: account},
	}
	fake := &fakeVerifier{claims: &Claims{Issuer: testIssuer, Aud: []string{"faas.example.com"}, Exp: time.Now().Add(5 * time.Minute)}}
	h := NewHandler(HandlerDeps{
		Verifier: patternVerifier{fake}, Policies: policies, Tokens: newMemTokenStore(),
		Lookups: lookup, Audit: &memAuditor{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if rr := exchangeOnce(t, h, other, "faas.example.com"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("other branch under an explicit policy: %d, want 401", rr.Code)
	}
}

// The account was found by repository name alone, so a repository recreated
// under a freed owner name exchanged for its deploy bearer. Verification now
// requires the immutable IDs the binding recorded (H68).
func TestServeHTTP_BoundRepositoryIDsAreRequired(t *testing.T) {
	account := state.Account{ID: testAcctID, Email: "octo@example.com", Plan: "free", Status: "active"}
	lookup := &repositoryIDLookup{
		bindingAwareLookup: bindingAwareLookup{stubAccountLookup: stubAccountLookup{bySubject: map[string]state.Account{testSub: account}}},
		ownerID:            583231, repoID: 1296269,
	}
	fake := &fakeVerifier{claims: &Claims{Issuer: testIssuer, Aud: []string{"faas.example.com"}, Exp: time.Now().Add(5 * time.Minute)}}
	h := NewHandler(HandlerDeps{
		Verifier: fake, Policies: newMemTrustPolicyStore(), Tokens: newMemTokenStore(),
		Lookups: lookup, Audit: &memAuditor{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if rr := exchangeOnce(t, h, testSub, "faas.example.com"); rr.Code != http.StatusOK {
		t.Fatalf("exchange: %d %s", rr.Code, rr.Body)
	}
	got := fake.lastPolicy.RequiredClaims
	if got["repository_owner_id"] != "583231" || got["repository_id"] != "1296269" {
		t.Fatalf("verified with required claims %v, want the binding's owner and repository IDs", got)
	}
}
