// iam6_org_keys_test.go — PR 6 acceptance (issue #190 / IAM-6 / ADR-061).
//
// Exercises the /v1/orgs/{slug}/keys/* surface (PR 6) end-to-end
// through the apid subprocess. The handlers route through
// pkg/authz.LoadOrg + AuthorizeOrgAction, so the test also pins the
// role-gate behaviour the dual-emit (legacy `key.created` +
// canonical `api_key.created`) audit contract relies on.
//
// Tests:
//
//   - TestE2E_OrgKeysList              — mint + list round-trip;
//     the response shape picks up the new org_id/status fields.
//   - TestE2E_OrgKeysCreate_MintAndRotate
//                                       — mint then rotate; the
//     rotated-from key is in 'grace' status with the successor
//     stamped as `rotated_from_id`.
//   - TestE2E_OrgKeysRevoke             — DELETE → 204; the key
//     now reads status='revoked' with revoked_at populated.
//   - TestE2E_OrgKeysGet_CrossOrgReturns404
//                                       — IDOR probe: account B
//     tries to read account A's key via A's slug in the path.
//     Store collapses cross-org reads to ErrNotFound → 404.
//   - TestE2E_OrgKeysAuthorisation      — two sub-cases for the
//     role gate (developer on POST → 403; viewer on DELETE → 403).
//   - TestE2E_LegacyKeysDualWrite       — POST /v1/keys (no
//     X-Active-Org) returns 201 with key_plaintext + org_id, AND
//     the audit log emits BOTH `key.created` and `api_key.created`
//     rows so legacy dashboards keep working through PR 9.
//
// All tests boot a dedicated apid so the seeded accounts don't bleed.
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS) and a buildable ./cmd/apid.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// listKeysWire mirrors cmd/apid/handlers_org_keys.go's
// ListOrgAPIKeysResponse so the test can decode the body without
// importing cmd/apid (which is package main, import-forbidden
// from cmd/e2e).
type listKeysWire struct {
	Keys []apiKeyWire `json:"keys"`
}

// apiKeyWire mirrors api.APIKeyResponse for the same reason. The
// fields below are the canonical wire — the SDK + dashboard render
// them 1:1.
type apiKeyWire struct {
	ID            string   `json:"id"`
	OrgID         string   `json:"org_id"`
	Prefix        string   `json:"prefix"`
	Label         string   `json:"label,omitempty"`
	Scopes        []string `json:"scopes"`
	CreatedAt     string   `json:"created_at"`
	LastUsedAt    string   `json:"last_used_at,omitempty"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
	Status        string   `json:"status,omitempty"`
	RevokedAt     string   `json:"revoked_at,omitempty"`
	RotatedFromID string   `json:"rotated_from_id,omitempty"`
	Plaintext     string   `json:"plaintext,omitempty"`
}

// rotateWire mirrors api.RotateOrgAPIKeyResponse.
type rotateWire struct {
	Key             apiKeyWire `json:"key"`
	KeyPlaintext    string     `json:"key_plaintext"`
	OldKeyID        string     `json:"old_key_id"`
	OldKeyExpiresAt string     `json:"old_key_expires_at"`
}

// personalOrgFor returns the personal org for the seeded account.
// e2e+<plan>+<label>@test.example is the deterministic email
// SeedAccount uses — see cmd/e2e/account_e2e_test.go::seedEmail.
func personalOrgFor(t *testing.T, h *e2etest.Harness, plan api.Plan, label string) state.Org {
	t.Helper()
	ctx := context.Background()
	store := state.NewPgStore(h.Pool)
	acct, err := store.AccountByEmail(ctx, seedEmail(plan, label))
	if err != nil {
		t.Fatalf("AccountByEmail(%s): %v", label, err)
	}
	org, err := store.OrgByPersonalAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount(%s): %v", label, err)
	}
	return org
}

// mintOrgKey POSTs /v1/orgs/{slug}/keys and returns the wire +
// status. Centralised so the six tests don't drift the body shape.
func mintOrgKey(t *testing.T, h *e2etest.Harness, key, slug string, label string, scopes []string) ([]byte, int) {
	t.Helper()
	return doReq(t, h, key, http.MethodPost, "/v1/orgs/"+slug+"/keys",
		api.CreateOrgAPIKeyRequest{Label: label, Scopes: scopes},
		map[string]string{"X-Active-Org": slug})
}

// TestE2E_OrgKeysList — happy path. After POST /v1/orgs/{slug}/keys,
// GET /v1/orgs/{slug}/keys returns exactly one key whose org_id
// matches the personal-org's id (proving the LoadOrg middleware
// stamped the membership onto the principal), with the new
// fields (OrgID, Status, etc.) populated on the wire.
func TestE2E_OrgKeysList(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanFree, "pr6-list")
	personal := personalOrgFor(t, h, api.PlanFree, "pr6-list")

	mintRaw, mintStatus := mintOrgKey(t, h, key, personal.Slug, "ci-deploy",
		[]string{api.ScopeAppsRead})
	if mintStatus != http.StatusCreated {
		t.Fatalf("mint: %d %s", mintStatus, mintRaw)
	}
	var minted apiKeyWire
	if err := json.Unmarshal(mintRaw, &minted); err != nil {
		t.Fatalf("decode mint: %v (body=%s)", err, mintRaw)
	}

	raw, status := doReq(t, h, key, http.MethodGet, "/v1/orgs/"+personal.Slug+"/keys", nil,
		map[string]string{"X-Active-Org": personal.Slug})
	if status != http.StatusOK {
		t.Fatalf("list: %d %s", status, raw)
	}
	var body listKeysWire
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode list: %v (body=%s)", err, raw)
	}
	// SeedAccount bootstraps a bootstrap-time "e2e" admin key on
	// every fresh account, so the org-scoped list contains at least
	// two rows: the seed key + the one we just minted. Locate the
	// minted row by ID rather than asserting the total count, so
	// the assertion survives harness evolution.
	var mintedListed *apiKeyWire
	for i := range body.Keys {
		if body.Keys[i].ID == minted.ID {
			mintedListed = &body.Keys[i]
			break
		}
	}
	if mintedListed == nil {
		t.Fatalf("minted key %q not in list (body=%s)", minted.ID, raw)
	}
	k := *mintedListed
	if k.ID != minted.ID {
		t.Errorf("id = %q, want %q", k.ID, minted.ID)
	}
	if k.OrgID != personal.ID {
		t.Errorf("org_id = %q, want %q (personal)", k.OrgID, personal.ID)
	}
	if k.Status != "active" {
		t.Errorf("status = %q, want active", k.Status)
	}
	if k.Prefix == "" {
		t.Errorf("prefix empty, want fp_live_…")
	}
	if k.CreatedAt == "" {
		t.Errorf("created_at empty")
	}
}

// TestE2E_OrgKeysCreate_MintAndRotate — the canonical rotation
// shape. POST creates an active key. POST /v1/orgs/{slug}/keys/{id}
// /rotate mints a new key (active), demotes the predecessor to
// 'grace' with an expires_at stamped by the rotation handler.
// The new key's RotatedFromID is the predecessor's id, and the
// rotation response carries the new plaintext + the old key id.
func TestE2E_OrgKeysCreate_MintAndRotate(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanFree, "pr6-rotate")
	personal := personalOrgFor(t, h, api.PlanFree, "pr6-rotate")

	mintRaw, mintStatus := mintOrgKey(t, h, key, personal.Slug, "ci-deploy",
		[]string{api.ScopeAppsRead, api.ScopeDeployWrite})
	if mintStatus != http.StatusCreated {
		t.Fatalf("mint: %d %s", mintStatus, mintRaw)
	}
	var minted apiKeyWire
	if err := json.Unmarshal(mintRaw, &minted); err != nil {
		t.Fatalf("decode mint: %v (body=%s)", err, mintRaw)
	}
	if minted.Plaintext == "" {
		t.Fatalf("mint response missing plaintext")
	}

	// Rotate. Body is the legacy {Label: ...} shape; an empty
	// label means "inherit the old label".
	rotateRaw, rotateStatus := doReq(t, h, key, http.MethodPost,
		"/v1/orgs/"+personal.Slug+"/keys/"+minted.ID+"/rotate",
		api.RotateOrgAPIKeyRequest{Label: ""},
		map[string]string{"X-Active-Org": personal.Slug})
	if rotateStatus != http.StatusOK {
		t.Fatalf("rotate: %d %s", rotateStatus, rotateRaw)
	}
	var rotated rotateWire
	if err := json.Unmarshal(rotateRaw, &rotated); err != nil {
		t.Fatalf("decode rotate: %v (body=%s)", err, rotateRaw)
	}
	if rotated.KeyPlaintext == "" {
		t.Errorf("rotate response missing key_plaintext")
	}
	if rotated.OldKeyID != minted.ID {
		t.Errorf("old_key_id = %q, want %q", rotated.OldKeyID, minted.ID)
	}
	if rotated.Key.ID == minted.ID {
		t.Errorf("new key id == old key id (%q)", rotated.Key.ID)
	}
	if rotated.Key.OrgID != personal.ID {
		t.Errorf("new key org_id = %q, want %q (personal)", rotated.Key.OrgID, personal.ID)
	}
	if rotated.Key.Status != "active" {
		t.Errorf("new key status = %q, want active", rotated.Key.Status)
	}
	if rotated.Key.RotatedFromID != minted.ID {
		t.Errorf("new key rotated_from_id = %q, want %q", rotated.Key.RotatedFromID, minted.ID)
	}

	// Confirm the old key is in grace: GET it directly through
	// the org route (the loadOrg middleware + membership gate
	// keep this IDOR-safe — the old key still belongs to the
	// personal org).
	getRaw, getStatus := doReq(t, h, key, http.MethodGet,
		"/v1/orgs/"+personal.Slug+"/keys/"+minted.ID, nil,
		map[string]string{"X-Active-Org": personal.Slug})
	if getStatus != http.StatusOK {
		t.Fatalf("get old key: %d %s", getStatus, getRaw)
	}
	var old apiKeyWire
	if err := json.Unmarshal(getRaw, &old); err != nil {
		t.Fatalf("decode old key: %v (body=%s)", err, getRaw)
	}
	if old.Status != "grace" {
		t.Errorf("old key status = %q, want grace", old.Status)
	}
	if old.RotatedFromID != "" {
		// Predecessor has no rotated_from_id — it was the
		// original mint.
		t.Errorf("predecessor rotated_from_id = %q, want empty", old.RotatedFromID)
	}
	if old.ExpiresAt == "" {
		t.Errorf("predecessor expires_at empty after rotation")
	}
}

// TestE2E_OrgKeysRevoke — DELETE returns 204, then GET on the
// revoked key shows status='revoked' with revoked_at populated.
// Revoke is idempotent at the store layer; the e2e here is the
// happy-path smoke.
func TestE2E_OrgKeysRevoke(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanFree, "pr6-revoke")
	personal := personalOrgFor(t, h, api.PlanFree, "pr6-revoke")

	mintRaw, mintStatus := mintOrgKey(t, h, key, personal.Slug, "ci-deploy",
		[]string{api.ScopeAppsRead})
	if mintStatus != http.StatusCreated {
		t.Fatalf("mint: %d %s", mintStatus, mintRaw)
	}
	var minted apiKeyWire
	if err := json.Unmarshal(mintRaw, &minted); err != nil {
		t.Fatalf("decode mint: %v", err)
	}

	delRaw, delStatus := doReq(t, h, key, http.MethodDelete,
		"/v1/orgs/"+personal.Slug+"/keys/"+minted.ID, nil,
		map[string]string{"X-Active-Org": personal.Slug})
	if delStatus != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", delStatus, delRaw)
	}

	// GET the revoked key. The listKeys handler filters
	// revoked rows out, but the singular get handler returns
	// them — the dashboard's "show revoked" view reads from
	// the singular GET.
	getRaw, getStatus := doReq(t, h, key, http.MethodGet,
		"/v1/orgs/"+personal.Slug+"/keys/"+minted.ID, nil,
		map[string]string{"X-Active-Org": personal.Slug})
	if getStatus != http.StatusOK {
		t.Fatalf("get revoked: %d %s", getStatus, getRaw)
	}
	var got apiKeyWire
	if err := json.Unmarshal(getRaw, &got); err != nil {
		t.Fatalf("decode revoked: %v", err)
	}
	if got.Status != "revoked" {
		t.Errorf("status = %q, want revoked", got.Status)
	}
	if got.RevokedAt == "" {
		t.Errorf("revoked_at empty, want RFC3339 timestamp")
	}
	// RFC3339 sanity — must be parseable.
	if _, err := time.Parse(time.RFC3339, got.RevokedAt); err != nil {
		t.Errorf("revoked_at %q: not RFC3339 (%v)", got.RevokedAt, err)
	}
}

// TestE2E_OrgKeysGet_CrossOrgReturns404 — IDOR probe. Account A
// mints a key on slugA. Account B (with personal slugB) tries
// to GET that key by hitting the slugB route with slugA's key
// id. The store's GetOrgAPIKey pins (id, org_id) — slugB's
// membership.OrgID != slugA's, so the SQL returns no row and
// the handler maps ErrNotFound → 404. The probe collapses
// silently; the customer can't tell whether the key exists
// under some other org.
func TestE2E_OrgKeysGet_CrossOrgReturns404(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	keyA := h.SeedAccount(ctx, api.PlanFree, "pr6-cross-a")
	keyB := h.SeedAccount(ctx, api.PlanFree, "pr6-cross-b")
	personalA := personalOrgFor(t, h, api.PlanFree, "pr6-cross-a")
	personalB := personalOrgFor(t, h, api.PlanFree, "pr6-cross-b")

	// A mints under slugA.
	mintRaw, mintStatus := mintOrgKey(t, h, keyA, personalA.Slug, "a-deploy",
		[]string{api.ScopeAppsRead})
	if mintStatus != http.StatusCreated {
		t.Fatalf("A mint: %d %s", mintStatus, mintRaw)
	}
	var minted apiKeyWire
	if err := json.Unmarshal(mintRaw, &minted); err != nil {
		t.Fatalf("decode mint: %v", err)
	}

	// B reads the same id via slugB. The store returns
	// ErrNotFound, the handler maps to 404. The probe should
	// not leak whether the key exists under slugA.
	raw, status := doReq(t, h, keyB, http.MethodGet,
		"/v1/orgs/"+personalB.Slug+"/keys/"+minted.ID, nil,
		map[string]string{"X-Active-Org": personalB.Slug})
	if status != http.StatusNotFound {
		t.Fatalf("cross-org read: status = %d, want 404 (body=%s)", status, raw)
	}

	// A can still read its own key — sanity-check the test
	// isn't accidentally broken by a global lockout.
	raw, status = doReq(t, h, keyA, http.MethodGet,
		"/v1/orgs/"+personalA.Slug+"/keys/"+minted.ID, nil,
		map[string]string{"X-Active-Org": personalA.Slug})
	if status != http.StatusOK {
		t.Errorf("A's own key: status = %d, want 200 (body=%s)", status, raw)
	}
}

// TestE2E_OrgKeysAuthorisation — the role matrix pin. Two
// sub-cases, both gated through the same authz pipeline:
//
//   - developer role on slugA tries POST /v1/orgs/slugA/keys.
//     OrgActionCreateApiKey is owner+admin only → 403
//     org_role_forbidden.
//
//   - viewer role on slugA tries DELETE /v1/orgs/slugA/keys/{id}
//     (after the owner mints a key). OrgActionRevokeApiKey is
//     owner+admin only → 403 org_role_forbidden.
//
// We pin these here (rather than unit-test the matrix) so the
// e2e suite catches a regression where a handler bypasses
// AuthorizeOrgAction and the role gate silently disappears.
func TestE2E_OrgKeysAuthorisation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	ownerKey := h.SeedAccount(ctx, api.PlanFree, "pr6-authz-owner")
	developerKey := h.SeedAccount(ctx, api.PlanFree, "pr6-authz-dev")
	viewerKey := h.SeedAccount(ctx, api.PlanFree, "pr6-authz-viewer")
	personal := personalOrgFor(t, h, api.PlanFree, "pr6-authz-owner")

	// Bring developer + viewer into the owner's personal org
	// at the appropriate role. Use SeedOrg + AddOrgMember so
	// the membership rows match what the loadOrg middleware
	// expects (account_id + role).
	store := state.NewPgStore(pool)
	acctDev, err := store.AccountByEmail(ctx, seedEmail(api.PlanFree, "pr6-authz-dev"))
	if err != nil {
		t.Fatalf("AccountByEmail dev: %v", err)
	}
	acctViewer, err := store.AccountByEmail(ctx, seedEmail(api.PlanFree, "pr6-authz-viewer"))
	if err != nil {
		t.Fatalf("AccountByEmail viewer: %v", err)
	}
	if err := store.AddOrgMember(ctx, personal.ID, acctDev.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatalf("AddOrgMember developer: %v", err)
	}
	if err := store.AddOrgMember(ctx, personal.ID, acctViewer.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatalf("AddOrgMember viewer: %v", err)
	}

	// Owner mints a key so we have an id to target with the
	// viewer's DELETE attempt.
	mintRaw, mintStatus := mintOrgKey(t, h, ownerKey, personal.Slug, "owner-deploy",
		[]string{api.ScopeAppsRead})
	if mintStatus != http.StatusCreated {
		t.Fatalf("owner mint: %d %s", mintStatus, mintRaw)
	}
	var minted apiKeyWire
	if err := json.Unmarshal(mintRaw, &minted); err != nil {
		t.Fatalf("decode owner mint: %v", err)
	}

	// Developer tries to mint. OrgActionCreateApiKey is
	// owner+admin; developer is denied. The middleware maps
	// the authz denial to a 403 with code=org_role_forbidden.
	t.Run("developer-cannot-mint", func(t *testing.T) {
		raw, status := mintOrgKey(t, h, developerKey, personal.Slug, "dev-deploy",
			[]string{api.ScopeAppsRead})
		if status != http.StatusForbidden {
			t.Fatalf("developer mint: status = %d, want 403 (body=%s)", status, raw)
		}
		if !strings.Contains(string(raw), "org_role_forbidden") {
			t.Errorf("body did not contain org_role_forbidden: %s", raw)
		}
	})

	// Viewer tries to revoke. OrgActionRevokeApiKey is
	// owner+admin; viewer is denied.
	t.Run("viewer-cannot-revoke", func(t *testing.T) {
		raw, status := doReq(t, h, viewerKey, http.MethodDelete,
			"/v1/orgs/"+personal.Slug+"/keys/"+minted.ID, nil,
			map[string]string{"X-Active-Org": personal.Slug})
		if status != http.StatusForbidden {
			t.Fatalf("viewer revoke: status = %d, want 403 (body=%s)", status, raw)
		}
		if !strings.Contains(string(raw), "org_role_forbidden") {
			t.Errorf("body did not contain org_role_forbidden: %s", raw)
		}
	})
}

// TestE2E_LegacyKeysDualWrite — POST /v1/keys with NO
// X-Active-Org header persists the key against the caller's
// personal org (api_keys.org_id populated) and emits BOTH the
// legacy `key.created` event and the new `api_key.created` event.
// PR 9 will collapse this to one event; the dual-emit keeps
// legacy dashboards working through the cutover.
//
// Reads the audit log via GET /v1/audit-events?kind_prefix=key.
// +api_key. — the same query the dashboard uses.
func TestE2E_LegacyKeysDualWrite(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanFree, "pr6-legacy")
	personal := personalOrgFor(t, h, api.PlanFree, "pr6-legacy")

	// No X-Active-Org header — the legacy /v1/keys path.
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/keys",
		api.CreateKeyRequest{Label: "ci-deploy",
			Scopes: []string{api.ScopeAppsRead, api.ScopeDeployWrite}})
	if status != http.StatusCreated {
		t.Fatalf("legacy mint: %d %s", status, raw)
	}
	var minted apiKeyWire
	if err := json.Unmarshal(raw, &minted); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, raw)
	}
	if minted.Plaintext == "" {
		t.Errorf("legacy mint missing plaintext")
	}
	if minted.OrgID == "" {
		t.Errorf("legacy mint org_id empty — PR 6 dual-write failed")
	}
	if minted.OrgID != personal.ID {
		t.Errorf("legacy mint org_id = %q, want %q (personal)", minted.OrgID, personal.ID)
	}

	// Pull the audit log. The kind_prefix filter is exact on
	// the prefix string, so we use "key." to also pick up
	// `key.created` rows (and any future `key.rotated`,
	// `key.revoked`). The new `api_key.created` rows share a
	// prefix of "api_key." which is a different prefix, so
	// this single query will only see the legacy `key.*`
	// rows. Two queries needed — one per prefix — because the
	// audit handler filter is exact, not OR.
	queries := []string{"key.", "api_key."}
	seen := map[string]bool{}
	for _, prefix := range queries {
		auditRaw, auditStatus := doReq(t, h, key, http.MethodGet,
			"/v1/audit-events?kind_prefix="+prefix, nil)
		if auditStatus != http.StatusOK {
			t.Fatalf("audit %s: %d %s", prefix, auditStatus, auditRaw)
		}
		var list api.ListAuditEventsResponse
		if err := json.Unmarshal(auditRaw, &list); err != nil {
			t.Fatalf("decode audit %s: %v (body=%s)", prefix, err, auditRaw)
		}
		for _, ev := range list.Events {
			seen[ev.Kind] = true
		}
	}
	if !seen["key.created"] {
		t.Errorf("audit log missing legacy key.created event (saw=%v)", seen)
	}
	if !seen["api_key.created"] {
		t.Errorf("audit log missing new api_key.created event (saw=%v)", seen)
	}
}
