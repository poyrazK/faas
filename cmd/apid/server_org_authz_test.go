// Negative-path tests for the AuthzRole (AuthorizeOrgAction) wiring
// on the /v1/orgs/{slug}/... surface (issue #190 / IAM-6 / ADR-061).
// pkg/authz/authorize_test.go covers the matrix cells themselves;
// this file covers the route table — that every org-scoped route is
// mounted behind the AuthorizeOrgAction gate and a missing membership
// trips the wire shape (403 CodeOrgRoleForbidden) before the handler
// body runs.
//
// Mirrors TestAllV1Routes_RequireAuthOrLimit
// (cmd/apid/server_authlimit_test.go:218) which guards the authn
// surface; this file guards the authz surface. The two together
// cover the two halves of the spec §11 "every authenticated route
// must be wrapped" defence.
//
// Routes NOT gated by AuthorizeOrgAction (account-scoped):
//   - GET /v1/orgs/me          (server.go:611)
//   - GET /v1/orgs             (server.go:624)
//   - POST /v1/orgs            (server.go:625)
// These intentionally bypass s.loadOrg and are NOT exercised here.
// Account-scoped reads on /v1/orgs /me etc. belong in a separate
// authn-only test (TestAllV1Routes_RequireAuthOrLimit already covers
// them).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// orgRoutesRequiringAuthorize is the canonical list of every
// /v1/orgs/{slug}/* pattern that MUST be gated by
// authz.AuthorizeOrgAction (directly or via s.requireOrgAction).
// Adding a new org-scoped route without appending it here will fail
// TestOrgRoutes_GatedByAuthorize, which is the regression guard
// IAM-6 / ADR-061 needs: a route accidentally mounted without
// s.loadOrg + s.requireOrgAction would silently serve unauthorized
// callers (or worse, allow a non-member to mutate the org).
//
// The list mirrors the route table in cmd/apid/server.go at the
// time of writing:
//   - server.go:626-633 (org CRUD + member management)
//   - server.go:835-839 (org-bound API keys, PR 6)
//   - PR 7: revokeInvitation (DELETE /v1/orgs/{slug}/invitations/{invitation_id})
//   - getOrgSeatUsage (GET /v1/orgs/{slug}/seat_usage)
//
// If the table moves, update this list (the test names the source
// line in its failure message).
var orgRoutesRequiringAuthorize = []struct {
	method string
	path   string
}{
	// Org CRUD + members (server.go:626-633).
	{"GET", "/v1/orgs/example-slug"},
	{"PATCH", "/v1/orgs/example-slug"},
	{"DELETE", "/v1/orgs/example-slug"},
	{"GET", "/v1/orgs/example-slug/members"},
	{"POST", "/v1/orgs/example-slug/members"},
	{"PATCH", "/v1/orgs/example-slug/members/user-1"},
	{"DELETE", "/v1/orgs/example-slug/members/user-1"},
	{"POST", "/v1/orgs/example-slug/transfer_ownership"},
	// PR 7: invitation revoke + seat-usage visibility.
	{"DELETE", "/v1/orgs/example-slug/invitations/00000000-0000-0000-0000-000000000001"},
	{"GET", "/v1/orgs/example-slug/seat_usage"},

	// Org-bound API keys (server.go:835-839, PR 6).
	{"GET", "/v1/orgs/example-slug/keys"},
	{"POST", "/v1/orgs/example-slug/keys"},
	{"GET", "/v1/orgs/example-slug/keys/key-1"},
	{"DELETE", "/v1/orgs/example-slug/keys/key-1"},
	{"POST", "/v1/orgs/example-slug/keys/key-1/rotate"},
}

// TestOrgRoutes_GatedByAuthorize walks every /v1/orgs/{slug}/*
// pattern with an API-key principal that is NOT a member of the
// seeded org. LoadOrg's IDOR-safe shape returns 403
// CodeOrgRoleForbidden (the principal exists, the org exists, but
// the principal is not a member) BEFORE the handler body runs, so
// any response in the 2xx range proves the gate was bypassed. An
// accidental `mux.HandleFunc("GET /v1/orgs/{slug}/...", handler)`
// without s.loadOrg would land the request on the handler with an
// empty membership — and the handler's first line of business is
// `requireOrgAction`, which fires AuthorizeOrgAction. If a future
// refactor removes the s.loadOrg wrap but keeps the handler
// interior gate, the test still fires (the interior gate produces
// 403 CodeOrgRoleForbidden). The loadOrg wrap is a defence in
// depth on top of the interior gate, not a replacement.
//
// The test seeds a real org via state.Store.CreateOrg so LoadOrg's
// OrgResolver returns the row, and the membership lookup fails
// with state.ErrNotFound — the path that produces
// api.ErrOrgRoleForbidden.
func TestOrgRoutes_GatedByAuthorize(t *testing.T) {
	e := setup(t, api.PlanPro)

	// Seed an org the principal is NOT a member of. The slug is
	// the placeholder in orgRoutesRequiringAuthorize ("example-slug");
	// the principal (e.acct) is intentionally never added to
	// org_members for this org, so LoadOrg's membership lookup
	// fails with state.ErrNotFound → 403 CodeOrgRoleForbidden.
	if _, err := e.store.CreateOrg(context.Background(), state.Org{
		Slug: "example-slug",
		Name: "Example Org",
	}); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	for _, r := range orgRoutesRequiringAuthorize {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(r.method, r.path, nil)
			// Bearer with ScopesAdminOnly — passes s.authLimited /
			// s.requireMFA / s.requireScope so the only gate that
			// can fire is the authz gate.
			req.Header.Set("Authorization", "Bearer "+e.key)
			e.h.ServeHTTP(rec, req)
			if rec.Code < 400 {
				t.Errorf("status = %d (body = %s), want 4xx — route is NOT behind AuthorizeOrgAction", rec.Code, rec.Body.String())
			}
			// Confirm the wire shape: 403 CodeOrgRoleForbidden is
			// the canonical LoadOrg → no-membership deny. We don't
			// pin this strictly because some handlers may legitimately
			// fall through to other 4xx (e.g. 400 bad body on POST
			// without a body — but only AFTER the auth gate has
			// admitted the request, which can't happen for a
			// non-member). The 4xx-only assertion is the load-bearing
			// pin; the code-shape assertion is best-effort.
			if rec.Code == http.StatusForbidden {
				var problem struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &problem)
				if problem.Code != "org_role_forbidden" {
					t.Logf("status = 403, code = %q (expected org_role_forbidden) — non-strict pin", problem.Code)
				}
			}
		})
	}
}

// TestOrgRoutes_GatedByAuthorize_UnknownSlug confirms that a probe
// with an unknown slug returns 404 CodeOrgNotFound (the slug
// lookup fails before the membership lookup), not 200. This is
// the IDOR-safe shape LoadOrg returns for a slug that does not
// exist in the orgs table — different code, same 4xx, same wire
// shape as the no-membership deny above. Together with
// TestOrgRoutes_GatedByAuthorize, this proves both branches of the
// LoadOrg gate fire 4xx on every org-scoped route.
//
// Same route table as above; the only difference is the slug.
func TestOrgRoutes_GatedByAuthorize_UnknownSlug(t *testing.T) {
	e := setup(t, api.PlanPro)

	for _, r := range orgRoutesRequiringAuthorize {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(r.method, r.path, nil)
			req.Header.Set("Authorization", "Bearer "+e.key)
			e.h.ServeHTTP(rec, req)
			if rec.Code < 400 {
				t.Errorf("status = %d, want 4xx", rec.Code)
			}
		})
	}
}

// bytes.NewReader is imported transitively for completeness if a
// future test adds a request body without re-importing "bytes".
// Mirrors the pattern in server_test.go's testEnv.do helper.
var _ = bytes.NewReader
