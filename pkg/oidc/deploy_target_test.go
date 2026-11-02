package oidc

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// deployTargetsLookup resolves the apps bound to the test repository.
type deployTargetsLookup struct {
	stubAccountLookup
	targets map[string]state.OIDCDeployTarget
}

func (l *deployTargetsLookup) OIDCDeployTargetForSubject(_ context.Context, _, _, _, app string) (state.OIDCDeployTarget, error) {
	target, ok := l.targets[app]
	if !ok {
		return state.OIDCDeployTarget{}, state.ErrNotFound
	}
	return target, nil
}

func deployGateHandler(t *testing.T, auditLogin string, claims Claims) (*Handler, *memTokenStore) {
	t.Helper()
	policies := newMemTrustPolicyStore()
	if _, err := policies.Upsert(context.Background(), &OIDCTrustPolicy{
		AccountID: testAcctID, IssuerURL: testIssuer, JWKSURL: testIssuer + ".well-known/jwks",
		Audience: []string{"faas.example.com"}, SubjectPattern: "^" + regexp.QuoteMeta(testSub) + "$",
		Algorithms: []string{"RS256"}, AuditLogin: auditLogin,
	}); err != nil {
		t.Fatal(err)
	}
	account := state.Account{ID: testAcctID, Email: "octo@example.com", Plan: "pro", Status: "active"}
	lookup := &deployTargetsLookup{
		stubAccountLookup: stubAccountLookup{bySubject: map[string]state.Account{testSub: account}},
		targets:           map[string]state.OIDCDeployTarget{"web": {AppID: "app-web", Branches: []string{"main", "staging"}}},
	}
	claims.Subject, claims.Issuer = testSub, testIssuer
	claims.Aud, claims.Exp = []string{"faas.example.com"}, time.Now().Add(5*time.Minute)
	tokens := newMemTokenStore()
	return NewHandler(HandlerDeps{
		Verifier: &fakeVerifier{claims: &claims}, Policies: policies, Tokens: tokens,
		Lookups: lookup, Audit: &memAuditor{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}), tokens
}

func exchangeFor(t *testing.T, h *Handler, app, capability string) *httptest.ResponseRecorder {
	t.Helper()
	body := mustJSON(t, ExchangeRequest{
		Provider: "github", Token: makeEnvelope(t, testIssuer, time.Now().Add(5*time.Minute)),
		Audience: "faas.example.com", App: app, Capability: capability,
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/v1/auth/oidc/exchange", bytes.NewReader(body)))
	return rr
}

// A policy Gregale created trusts every workflow of a bound repository
// (ADR-360). Its deploy bearer went to any branch and to pull request runs,
// account-wide, so a pull request deployed unreviewed code to production of
// any app. It now goes to the repository's deploy branches and v* release
// tags, outside pull_request events, for one app bound to the repository,
// and is bound to that app.
func TestServeHTTP_GregalePolicyDeploysOnlyFromDeployRefs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ref, how  string
		app       string
		want      int
		wantBound string
	}{
		{name: "production branch", ref: "refs/heads/main", how: "push", app: "web", want: http.StatusOK, wantBound: "app-web"},
		{name: "deploy branch", ref: "refs/heads/staging", how: "push", app: "web", want: http.StatusOK, wantBound: "app-web"},
		{name: "release tag", ref: "refs/tags/v1.4.0", how: "push", app: "web", want: http.StatusOK, wantBound: "app-web"},
		{name: "other branch", ref: "refs/heads/feature-7", how: "push", app: "web", want: http.StatusForbidden},
		{name: "pull request run", ref: "refs/pull/7/merge", how: "pull_request", app: "web", want: http.StatusForbidden},
		{name: "pull_request_target on main", ref: "refs/heads/main", how: "pull_request_target", app: "web", want: http.StatusForbidden},
		{name: "no app named", ref: "refs/heads/main", how: "push", app: "", want: http.StatusForbidden},
		{name: "app of another repository", ref: "refs/heads/main", how: "push", app: "billing", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, tokens := deployGateHandler(t, autoPolicyLogin, Claims{Ref: tc.ref, EventName: tc.how})
			rr := exchangeFor(t, h, tc.app, "")
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.want, rr.Body)
			}
			if tc.want != http.StatusOK {
				if len(tokens.tokens) != 0 {
					t.Fatalf("a refused exchange stored %d bearers", len(tokens.tokens))
				}
				return
			}
			for _, token := range tokens.tokens {
				if token.AppID != tc.wantBound {
					t.Fatalf("bearer bound to %q, want %q", token.AppID, tc.wantBound)
				}
			}
		})
	}
}

// A policy the customer wrote is authoritative (bug hunt 2026-10-28 H46): its
// bearers keep the account-wide deploy scope from any ref it admits.
func TestServeHTTP_CustomerPolicyKeepsAccountWideBearer(t *testing.T) {
	h, tokens := deployGateHandler(t, "octo@example.com", Claims{Ref: "refs/heads/feature-7", EventName: "push"})
	if rr := exchangeFor(t, h, "", ""); rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body)
	}
	for _, token := range tokens.tokens {
		if token.AppID != "" {
			t.Fatalf("customer policy bearer bound to %q, want account-wide", token.AppID)
		}
	}
}

// The environment-preflight capability reads and qualifies; it cannot deploy,
// so a pull request run may still use it.
func TestServeHTTP_PreflightFromPullRequestIsAllowed(t *testing.T) {
	h, _ := deployGateHandler(t, autoPolicyLogin, Claims{Ref: "refs/pull/7/merge", EventName: "pull_request"})
	if rr := exchangeFor(t, h, "", CapabilityEnvironmentPreflight); rr.Code != http.StatusOK {
		t.Fatalf("preflight from a pull request = %d; body=%s", rr.Code, rr.Body)
	}
}
