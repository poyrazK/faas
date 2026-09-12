// load_org_e2e_test.go — PR 4 acceptance (issue #190 / IAM-6 / ADR-061).
// Exercises pkg/authz.LoadOrg end-to-end through the apid subprocess:
// the new GET /v1/orgs/me endpoint is the seam that surfaces the
// X-Active-Org / ?org= resolution path so the middleware can be
// observed before PR 5 lands the rest of the org CRUD surface.
//
// Tests:
//
//   - TestE2E_LoadOrg_PersonalOrg      signup creates a personal org,
//     GET /v1/orgs/me returns it with role=owner.
//   - TestE2E_LoadOrg_HeaderMissDefaultsToPersonalOrg
//     no X-Active-Org / ?org= → the caller's personal org.
//   - TestE2E_LoadOrg_UnknownSlug      unknown slug → 404 org_not_found.
//   - TestE2E_LoadOrg_NonMember        account B's personal slug from
//     account A's session → 403 org_role_forbidden (IDOR-safe).
//   - TestE2E_LoadOrg_QueryFallback    ?org=<slug> resolves identically
//     to the header form.
//   - TestE2E_LoadOrg_HeaderBeatsQuery header takes precedence when both
//     are present; a bad header overrides a good query.
//
// All tests boot a dedicated apid so the seeded accounts don't bleed.
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS) and a buildable ./cmd/apid.
//
// Without Postgres (e.g. a dev box without a Postgres service
// container) pgtest.OpenMigrated(t) returns nil at every test entry and
// the tests skip — exactly the same pattern as
// personal_org_backfill_e2e_test.go (PR 3). The unit tests in
// pkg/authz/loadorg_test.go cover the middleware per-request
// semantics with httptest + a stub resolver so the LoadOrg path
// is exercised at `make test` even without Postgres.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// orgMeWire mirrors cmd/apid/handlers_org_me.go::orgMeResponse so the
// test can decode the body without importing cmd/apid (which is a
// package main, import-forbidden from cmd/e2e).
type orgMeWire struct {
	Org *struct {
		ID       string `json:"id"`
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		Personal bool   `json:"personal"`
		Role     string `json:"role"`
	} `json:"org"`
}

// TestE2E_LoadOrg_PersonalOrg — the most basic round-trip. A fresh
// signup mints a personal org + owner membership (PR 3 / migration
// 00105 + CreateAccountWithPersonalOrg dual-write). Without any
// header the middleware passes through; with X-Active-Org set to
// the personal slug the endpoint returns the org with role=owner.
func TestE2E_LoadOrg_PersonalOrg(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanFree, "pr4-personal")

	// Look up the personal-org slug (the LoadOrg header accepts a slug,
	// not an ID) so we can drive the test from a known value rather than
	// guessing at the deterministic "u-<12hex>" pattern.
	store := state.NewPgStore(pool)
	acct, err := store.AccountByEmail(ctx, seedEmail(api.PlanFree, "pr4-personal"))
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	personal, err := store.OrgByPersonalAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount: %v", err)
	}

	// Drive the route through the load-bearing path: header set,
	// query absent, expect the personal org + role=owner.
	raw, status := doReqWithHeaders(t, h, key, http.MethodGet, "/v1/orgs/me", nil, map[string]string{
		"X-Active-Org": personal.Slug,
	})
	if status != http.StatusOK {
		t.Fatalf("personal-org response: %d %s", status, raw)
	}
	var body orgMeWire
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, raw)
	}
	if body.Org == nil {
		t.Fatalf("org = null, want personal org")
	}
	if body.Org.Slug != personal.Slug {
		t.Errorf("slug = %q, want %q", body.Org.Slug, personal.Slug)
	}
	if body.Org.Role != string(state.OrgRoleOwner) {
		t.Errorf("role = %q, want owner", body.Org.Role)
	}
	if !body.Org.Personal {
		t.Errorf("personal = false, want true")
	}
}

// TestE2E_LoadOrg_HeaderMissDefaultsToPersonalOrg — no explicit hint keeps
// middleware account-scoped, then /v1/orgs/me resolves the caller's canonical
// personal organization. This makes the endpoint useful to normal CLI clients
// without requiring them to synthesize an X-Active-Org header.
func TestE2E_LoadOrg_HeaderMissDefaultsToPersonalOrg(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanFree, "pr4-miss")

	raw, status := doReq(t, h, key, http.MethodGet, "/v1/orgs/me", nil)
	if status != http.StatusOK {
		t.Fatalf("default-personal-org status: %d %s", status, raw)
	}
	var body orgMeWire
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, raw)
	}
	if body.Org == nil {
		t.Fatal("org = null, want caller's personal org")
	}
	if !body.Org.Personal || body.Org.Role != string(state.OrgRoleOwner) {
		t.Errorf("org = %+v, want personal org with owner role", body.Org)
	}
}

// TestE2E_LoadOrg_UnknownSlug — an org that doesn't exist must
// return 404 with the org_not_found code. The resolver returns
// state.ErrNotFound from OrgBySlug and the middleware maps that
// to api.ErrOrgNotFound. This is the IDOR-safe behaviour the rest
// of the platform relies on: "does not exist" is a distinct 4xx
// from "exists but you can't see it".
func TestE2E_LoadOrg_UnknownSlug(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanFree, "pr4-unknown")

	raw, status := doReqWithHeaders(t, h, key, http.MethodGet, "/v1/orgs/me", nil, map[string]string{
		"X-Active-Org": "does-not-exist",
	})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", status, raw)
	}
	if !strings.Contains(string(raw), "org_not_found") {
		t.Errorf("body did not contain org_not_found: %s", raw)
	}
}

// TestE2E_LoadOrg_NonMember — the IDOR probe. Account A and
// account B are independent. Account A tries to act under
// account B's personal slug. OrgBySlug finds the row, but
// OrgMemberByAccount returns ErrNotFound (B's owner membership
// doesn't include A). The middleware maps that to 403
// org_role_forbidden. This is the test that proves LoadOrg
// is membership-aware, not slug-string-equality.
func TestE2E_LoadOrg_NonMember(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	keyA := h.SeedAccount(ctx, api.PlanFree, "pr4-a")
	h.SeedAccount(ctx, api.PlanFree, "pr4-b")

	store := state.NewPgStore(pool)
	acctB, err := store.AccountByEmail(ctx, seedEmail(api.PlanFree, "pr4-b"))
	if err != nil {
		t.Fatalf("AccountByEmail B: %v", err)
	}
	personalB, err := store.OrgByPersonalAccount(ctx, acctB.ID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount B: %v", err)
	}

	raw, status := doReqWithHeaders(t, h, keyA, http.MethodGet, "/v1/orgs/me", nil, map[string]string{
		"X-Active-Org": personalB.Slug,
	})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", status, raw)
	}
	if !strings.Contains(string(raw), "org_role_forbidden") {
		t.Errorf("body did not contain org_role_forbidden: %s", raw)
	}
}

// TestE2E_LoadOrg_QueryFallback — same resolution as the header
// path, driven by ?org=<slug>. PR 4 ships both surfaces per the
// "header preferred, query fallback" rule; the query path is the
// one browsers can hit naturally (e.g. a dashboard link from an
// email).
func TestE2E_LoadOrg_QueryFallback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanFree, "pr4-query")

	store := state.NewPgStore(pool)
	acct, err := store.AccountByEmail(ctx, seedEmail(api.PlanFree, "pr4-query"))
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	personal, err := store.OrgByPersonalAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount: %v", err)
	}

	raw, status := doReq(t, h, key, http.MethodGet, "/v1/orgs/me?org="+personal.Slug, nil)
	if status != http.StatusOK {
		t.Fatalf("query fallback status: %d %s", status, raw)
	}
	var body orgMeWire
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, raw)
	}
	if body.Org == nil {
		t.Fatalf("org = null, want personal org via query fallback")
	}
	if body.Org.Slug != personal.Slug {
		t.Errorf("slug = %q, want %q", body.Org.Slug, personal.Slug)
	}
}

// TestE2E_LoadOrg_HeaderBeatsQuery — when both are present, the
// header is the source of truth (per ADR-061's "header preferred"
// wording). The query string is the fallback for environments
// that can't set headers (HTML <a>, email links, etc.). A bad
// header must NOT be silently rescued by a good query.
func TestE2E_LoadOrg_HeaderBeatsQuery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanFree, "pr4-precedence")

	store := state.NewPgStore(pool)
	acct, err := store.AccountByEmail(ctx, seedEmail(api.PlanFree, "pr4-precedence"))
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	personal, err := store.OrgByPersonalAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount: %v", err)
	}

	// Bad header + good query → 404 (header wins). Proves the
	// query is not silently used as a fallback when the header
	// is present but invalid.
	raw, status := doReqWithHeaders(t, h, key, http.MethodGet,
		"/v1/orgs/me?org="+personal.Slug, nil, map[string]string{
			"X-Active-Org": "does-not-exist",
		})
	if status != http.StatusNotFound {
		t.Errorf("bad header + good query: status = %d, want 404 (body=%s)", status, raw)
	}
}

// doReqWithHeaders is a thin wrapper over doReq that lets the
// caller set request headers (X-Active-Org in particular). Keeps
// the LoadOrg header surface co-located with this file's tests;
// the actual request shape lives in quota_e2e_test.go::doReq so
// changes to the auth/Content-Type headers don't drift between
// the two files.
func doReqWithHeaders(t *testing.T, h *e2etest.Harness, key, method, path string, body any, headers map[string]string) ([]byte, int) {
	t.Helper()
	return doReq(t, h, key, method, path, body, headers)
}
