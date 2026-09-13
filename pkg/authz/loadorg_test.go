// loadorg_test.go — unit tests for LoadOrgWithResolver. These
// drive the per-request body with httptest.NewRecorder + a stub
// OrgResolver so the middleware can be exercised without booting
// the apid subprocess or a Postgres service container. The e2e
// probes in cmd/e2e/load_org_e2e_test.go cover the same surface
// end-to-end; these tests cover the per-request semantics so a
// future regression in the resolver path surfaces at `make test`
// rather than CI.
//
// Issue #190 / IAM-6 / ADR-061, PR 4.

package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// stubResolver is the in-memory OrgResolver used by every test
// in this file. The OrgBySlug + OrgMemberByAccount maps let each
// test pre-load exactly the row(s) it wants resolved; reads
// default to state.ErrNotFound so unknown-slug tests get the
// same path production sees.
type stubResolver struct {
	orgs    map[string]state.Org
	members map[string]state.OrgMembership
}

func newStubResolver() *stubResolver {
	return &stubResolver{
		orgs:    map[string]state.Org{},
		members: map[string]state.OrgMembership{},
	}
}

func (s *stubResolver) OrgBySlug(_ context.Context, slug string) (state.Org, error) {
	o, ok := s.orgs[slug]
	if !ok {
		return state.Org{}, state.ErrNotFound
	}
	return o, nil
}

func (s *stubResolver) OrgMemberByAccount(_ context.Context, orgID, accountID string) (state.OrgMembership, error) {
	key := orgID + "|" + accountID
	m, ok := s.members[key]
	if !ok {
		return state.OrgMembership{}, state.ErrNotFound
	}
	return m, nil
}

// reqWithPrincipal builds an *http.Request with a principal
// stamped via pkg/auth/middleware — the same seam RequireSession
// uses in production. The test then drives LoadOrgWithResolver
// directly without booting the auth chain.
func reqWithPrincipal(t *testing.T, method, path string, header string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	acct := state.Account{ID: "acct-1"}
	req = req.WithContext(authmw.WithPrincipal(req.Context(), acct, nil, nil))
	if header != "" {
		req.Header.Set("X-Active-Org", header)
	}
	return req
}

// noopHandler is the next handler LoadOrgWithResolver wraps when
// driving tests. It records pass-through so the test can assert
// whether the middleware forwarded the request or wrote a
// problem response.
type noopHandler struct {
	called             bool
	membershipObserved *state.OrgMembership
}

func (n *noopHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.called = true
	// Mirror the production read site: MembershipFrom is what
	// the rest of the cmd/apid route table uses to observe the
	// stamped membership.
	if mem, ok := MembershipFrom(r); ok {
		n.membershipObserved = mem
	}
	w.WriteHeader(http.StatusOK)
}

// runLoadOrg drives a single LoadOrg cycle with the given req +
// stub resolver + fake audit. Returns the recorder + the next
// handler so the test can assert both problem responses and
// pass-through state.
func runLoadOrg(t *testing.T, req *http.Request, resolver OrgResolver, audit *fakeAudit) (*httptest.ResponseRecorder, *noopHandler) {
	t.Helper()
	cfg := LoadOrgConfig{
		HeaderName: "X-Active-Org",
		QueryName:  "org",
		Audit:      audit,
	}
	mw := LoadOrgWithResolver(cfg, resolver)
	next := &noopHandler{}
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	return rec, next
}

// TestLoadOrg_NilPrincipal_FailsClosed — RequireSession didn't run
// (no principal stamped on the request). LoadOrg must write 500
// CodeCapacity, not call the next handler. 500 is correct: this
// is a wiring bug, not a user error, and a 403 would silently hide
// it.
func TestLoadOrg_NilPrincipal_FailsClosed(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/orgs/me", nil)
	// No principal stamped.
	rec, next := runLoadOrg(t, req, newStubResolver(), nil)
	if next.called {
		t.Fatal("next handler called without principal; want 500")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), api.CodeCapacity) {
		t.Errorf("body = %q, want code %q", rec.Body.String(), api.CodeCapacity)
	}
}

// TestLoadOrg_PassthroughNoSlug — neither header nor query set →
// LoadOrg forwards the request with no membership stamped. The
// /v1/orgs/me route relies on this for the "no active org" case.
func TestLoadOrg_PassthroughNoSlug(t *testing.T) {
	req := reqWithPrincipal(t, "GET", "/v1/orgs/me", "" /* no header */)
	rec, next := runLoadOrg(t, req, newStubResolver(), nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !next.called {
		t.Fatal("next handler not called; LoadOrg should pass through when no slug is set")
	}
	if next.membershipObserved != nil {
		t.Errorf("membership = %+v, want nil", next.membershipObserved)
	}
}

func TestLoadOrg_UsesRouteSlugWithoutDuplicateHeader(t *testing.T) {
	resolver := newStubResolver()
	resolver.orgs["acme"] = state.Org{ID: "org-1", Slug: "acme"}
	resolver.members["org-1|acct-1"] = state.OrgMembership{
		OrgID: "org-1", AccountID: "acct-1", Role: state.OrgRoleOwner,
	}
	req := reqWithPrincipal(t, http.MethodGet, "/v1/orgs/acme", "")
	req.SetPathValue("slug", "acme")
	rec, next := runLoadOrg(t, req, resolver, nil)
	if rec.Code != http.StatusOK || !next.called {
		t.Fatalf("route-slug request = %d, next=%v", rec.Code, next.called)
	}
	if next.membershipObserved == nil || next.membershipObserved.OrgID != "org-1" {
		t.Fatalf("membership = %+v, want org-1", next.membershipObserved)
	}
}

// TestLoadOrg_HeaderPreferredOverQuery — both header and query set
// → header wins. The good header resolves; the bad query is
// ignored. This is the IDOR-safe precedence: a malicious query
// string can't override a legit header.
func TestLoadOrg_HeaderPreferredOverQuery(t *testing.T) {
	resolver := newStubResolver()
	resolver.orgs["good"] = state.Org{ID: "org-1", Slug: "good"}
	resolver.orgs["bad"] = state.Org{ID: "org-bad", Slug: "bad"}
	resolver.members["org-1|acct-1"] = state.OrgMembership{OrgID: "org-1", AccountID: "acct-1", Role: state.OrgRoleOwner}

	req := httptest.NewRequest("GET", "/v1/orgs/me?org=bad", nil)
	acct := state.Account{ID: "acct-1"}
	req = req.WithContext(authmw.WithPrincipal(req.Context(), acct, nil, nil))
	req.Header.Set("X-Active-Org", "good")

	rec, next := runLoadOrg(t, req, resolver, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !next.called {
		t.Fatal("next handler not called")
	}
	if next.membershipObserved == nil || next.membershipObserved.OrgID != "org-1" {
		t.Errorf("membership = %+v, want OrgID=org-1 (header should win)", next.membershipObserved)
	}
}

// TestLoadOrg_UnknownSlug_404 — resolver returns state.ErrNotFound
// for OrgBySlug → LoadOrg writes 404 org_not_found and emits one
// audit row. The audit shape must include the slug in the
// "msg"-style fields so pkg/audit's downstream consumers can
// group by slug.
func TestLoadOrg_UnknownSlug_404(t *testing.T) {
	resolver := newStubResolver() // empty: any slug → ErrNotFound
	audit := &fakeAudit{}
	req := reqWithPrincipal(t, "GET", "/v1/orgs/me", "no-such-org")
	rec, next := runLoadOrg(t, req, resolver, audit)

	if next.called {
		t.Fatal("next handler called for unknown slug; want 404")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeOrgNotFound) {
		t.Errorf("body = %q, want code %q", rec.Body.String(), api.CodeOrgNotFound)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	if audit.events[0].event != "authz.denied" {
		t.Errorf("event = %q, want authz.denied", audit.events[0].event)
	}
	if audit.events[0].fields["action"] != "org.load.not_found" {
		t.Errorf("fields[action] = %v, want org.load.not_found", audit.events[0].fields["action"])
	}
}

// TestLoadOrg_NonMember_403 — OrgBySlug OK but OrgMemberByAccount
// returns ErrNotFound → 403 org_role_forbidden. IDOR-safe: the
// response shape is the same as 404 except the code, so an
// attacker enumerating slugs cannot distinguish "unknown" from
// "known but not a member".
func TestLoadOrg_NonMember_403(t *testing.T) {
	resolver := newStubResolver()
	resolver.orgs["acme"] = state.Org{ID: "org-1", Slug: "acme"}
	// No member row for acct-1 in org org-1.
	audit := &fakeAudit{}
	req := reqWithPrincipal(t, "GET", "/v1/orgs/me", "acme")
	rec, next := runLoadOrg(t, req, resolver, audit)

	if next.called {
		t.Fatal("next handler called for non-member; want 403")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeOrgRoleForbidden) {
		t.Errorf("body = %q, want code %q", rec.Body.String(), api.CodeOrgRoleForbidden)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	if audit.events[0].fields["action"] != "org.load.not_member" {
		t.Errorf("fields[action] = %v, want org.load.not_member", audit.events[0].fields["action"])
	}
}

// TestLoadOrg_SlugTrimAndCap — whitespace-only and oversize
// values must degrade to passthrough. The schema's CHECK
// (migrations/00099) rejects oversize values with a non-
// ErrNotFound error, which would surface as 500; the length cap
// pushes the test below that boundary. Trim must run so
// "  acme  " passes through to "acme" (mirroring how the
// pkg/api.OrgSlugPattern regex is matched case-insensitively
// elsewhere).
func TestLoadOrg_SlugTrimAndCap(t *testing.T) {
	t.Run("whitespace-only header passthrough", func(t *testing.T) {
		req := reqWithPrincipal(t, "GET", "/v1/orgs/me", "   ")
		rec, next := runLoadOrg(t, req, newStubResolver(), nil)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (passthrough); body=%s", rec.Code, rec.Body.String())
		}
		if !next.called {
			t.Fatal("next handler not called; want passthrough")
		}
	})

	t.Run("oversize slug passthrough", func(t *testing.T) {
		// 65 chars exceeds maxSlugLen (64) but is well below HTTP
		// header limits; the cap is what protects the SQL CHECK.
		oversize := strings.Repeat("a", 65)
		req := reqWithPrincipal(t, "GET", "/v1/orgs/me", oversize)
		rec, next := runLoadOrg(t, req, newStubResolver(), nil)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (passthrough); body=%s", rec.Code, rec.Body.String())
		}
		if !next.called {
			t.Fatal("next handler not called; want passthrough")
		}
	})

	t.Run("padded slug resolves", func(t *testing.T) {
		resolver := newStubResolver()
		resolver.orgs["acme"] = state.Org{ID: "org-1", Slug: "acme"}
		resolver.members["org-1|acct-1"] = state.OrgMembership{OrgID: "org-1", AccountID: "acct-1", Role: state.OrgRoleOwner}
		req := reqWithPrincipal(t, "GET", "/v1/orgs/me", "  acme  ")
		rec, next := runLoadOrg(t, req, resolver, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if !next.called {
			t.Fatal("next handler not called")
		}
		if next.membershipObserved == nil || next.membershipObserved.OrgID != "org-1" {
			t.Errorf("membership = %+v, want OrgID=org-1", next.membershipObserved)
		}
	})
}

// TestLoadOrg_QueryMalformed_PassThrough — URLs encode %00 %01 %02
// as literal bytes after URL-decoding; the schema rejects them
// with a non-ErrNotFound error. The trim+length-cap path
// already protects against oversize; the breakage is for empty
// bytes between valid chars (e.g. "%20%20%20" → "   " — whitespace
// that TrimSpace strips). The trim_to_empty path falls through to
// the passthrough branch, so the missing-org surface is never
// reached. This test pins that behaviour.
func TestLoadOrg_QueryMalformed_PassThrough(t *testing.T) {
	req := reqWithPrincipal(t, "GET", "/v1/orgs/me?org=%20%20%20", "")
	rec, next := runLoadOrg(t, req, newStubResolver(), nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (passthrough); body=%s", rec.Code, rec.Body.String())
	}
	if !next.called {
		t.Fatal("next handler not called; want passthrough")
	}
}

// stubBrokenResolver returns a non-ErrNotFound error from OrgBySlug
// so the middleware takes the "lookup failed" branch (the only branch
// that emits a Warn log). The empty-org stubResolver returns
// state.ErrNotFound which is the 404 path — that doesn't reach the
// log line at all, so we need a separate fake to exercise the
// sanitization gate.
type stubBrokenResolver struct{}

func (stubBrokenResolver) OrgBySlug(_ context.Context, _ string) (state.Org, error) {
	return state.Org{}, errors.New("synthetic pg outage")
}
func (stubBrokenResolver) OrgMemberByAccount(_ context.Context, _, _ string) (state.OrgMembership, error) {
	return state.OrgMembership{}, state.ErrNotFound
}

// TestLoadOrg_LogDoesNotLeakSlug — CodeQL probes #152-156 fired on the
// org-lookup-failed Warn line because the slug flows from the
// X-Active-Org header (an attacker-controlled source) directly into
// slog. The structural fix is two-part:
//
//  1. The slug is NOT logged at all in the org-lookup-failed path —
//     CodeQL's go/clear-text-logging rule (CWE-200) treats any HTTP
//     header / URL query parameter flowing to a log call as a
//     finding, and the rule's barrier model only honours header.Get
//     for headers OTHER than Authorization/Cookie (X-Active-Org
//     matches the barrier) but url.Values.Get has no barrier at
//     all, so any logged slug re-opens the alert. The slug is
//     already captured in the audit row (org.load.not_found →
//     ErrOrgNotFound(slug)) so the Warn can drop it.
//
//  2. account_id is hashed via logHashShort before reaching slog —
//     logHashShort's name matches the CodeQL notSensitive() regex
//     (which includes "hash"), so it's recognised as an obfuscator
//     barrier. The output is `h:<16 hex chars>`, a fixed-shape
//     prefix that disambiguates per-account log entries without
//     ever exposing the raw account id. The "h:" prefix is the
//     canonical signal to a human reader that the value is a
//     fingerprint, not a literal id.
//
// This test pins both contracts. The synthetic resolver error
// is what makes the test cover the Warn branch — the empty
// stubResolver returns state.ErrNotFound, which goes through the
// 404 path (no log line).
func TestLoadOrg_LogDoesNotLeakSlug(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	cfg := LoadOrgConfig{
		HeaderName: "X-Active-Org",
		QueryName:  "org",
		Log:        logger,
	}
	req := reqWithPrincipal(t, "GET", "/v1/orgs/me", "acme\nfake-line")
	mw := LoadOrgWithResolver(cfg, stubBrokenResolver{})
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)

	// The middleware should have written a 500 — and the log line
	// must NOT contain the raw slug, even after sanitization.
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	out := logBuf.String()
	if out == "" {
		t.Fatal("expected a log line; got empty buffer")
	}

	// Decode the JSON log line so we can inspect fields directly
	// (instead of grepping for substrings across the whole JSON).
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &entry); err != nil {
		t.Fatalf("log line not JSON: %v (raw=%q)", err, out)
	}

	// Contract 1: no "slug" field in the log line. (Earlier
	// revisions of this test asserted the sanitized slug was
	// present; the structural fix removes the slug entirely.)
	if _, hasSlug := entry["slug"]; hasSlug {
		t.Errorf("log line still contains a slug field: %+v", entry)
	}
	// Belt-and-suspenders: even with the slug field removed, a
	// copy of the raw header value must not appear anywhere in
	// the JSON output. A malicious header cannot forge log lines
	// by smuggling CR/LF into a value that gets concatenated
	// into the log stream.
	if strings.Contains(out, "acme\nfake-line") {
		t.Errorf("raw header value (acme\\nfake-line) found in log output: %q", out)
	}

	// Contract 2: account_id_hash is present, has the "h:" prefix,
	// is 18 chars long (h: + 16 hex), and contains no raw account
	// id. The hash is deterministic for the same input, so two
	// runs of the test produce the same fingerprint.
	hashField, ok := entry["account_id_hash"].(string)
	if !ok {
		t.Fatalf("account_id_hash field missing or not a string: %+v", entry)
	}
	if !strings.HasPrefix(hashField, "h:") {
		t.Errorf("account_id_hash = %q, want h: prefix", hashField)
	}
	if len(hashField) != len("h:")+16 {
		t.Errorf("account_id_hash = %q, want h: + 16 hex chars (got %d)", hashField, len(hashField))
	}
	// The raw account id is "acct-1" (set by reqWithPrincipal);
	// it must not appear in the hash output.
	if strings.Contains(hashField, "acct-1") {
		t.Errorf("account_id_hash leaks raw account id: %q", hashField)
	}
}
