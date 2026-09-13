// Org-bound API keys — issue #190 / IAM-6 / ADR-061, PR 6.
//
// The five handlers here wire the /v1/orgs/{slug}/keys/* surface
// that supersedes the /v1/keys family for customers who migrate
// their personal-orgs to multi-org accounts. PR 9 collapses the
// legacy routes; PR 6 ships them side-by-side so the SDK + CLI can
// flip one call site at a time.
//
// The handlers route through:
//
//   - pkg/authz.LoadOrg (cmd/apid/loadorg_wiring.go) — stamps the
//     active-org membership onto the principal before the handler
//     runs. The membership's OrgID is the canonical bind for every
//     store call below; the route table's slug is the lookup index
//     only.
//   - pkg/authz.AuthorizeOrgAction — the closed-vocabulary role
//     gate. Owner + admin for the two sensitive verbs
//     (org.create_api_key, org.revoke_api_key); every role can
//     view.
//   - state.Store.CreateOrgAPIKey / ListOrgAPIKeys / GetOrgAPIKey /
//     RevokeOrgAPIKey / RotateOrgAPIKey — the IDOR-safe accessors
//     that pin (id, org_id) on every read, collapsing cross-org
//     probes to ErrNotFound.
//
// Audit convention: every emit here writes the new PR 6
// `api_key.{created,rotated,revoked}` event. The legacy
// `key.{created,rotated,revoked}` events still fire from the
// pre-PR-6 /v1/keys handlers (with dual-emit on those code paths)
// so the legacy dashboards keep working through PR 9. PR 9 drops
// the legacy events + the legacy routes.
//
// Wire-shape (response bodies):
//
//   - GET    /v1/orgs/{slug}/keys
//     {"keys": [APIKeyResponse, ...]}
//   - POST   /v1/orgs/{slug}/keys
//     APIKeyResponse (with plaintext)
//   - GET    /v1/orgs/{slug}/keys/{id}
//     APIKeyResponse
//   - DELETE /v1/orgs/{slug}/keys/{id}
//     204 (no body)
//   - POST   /v1/orgs/{slug}/keys/{id}/rotate
//     RotateOrgAPIKeyResponse (with key_plaintext)
package main

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// authzAuditForCurrentRequest converts the server's *auditor into
// the authz.AuditEmitter interface so AuthorizeOrgAction can emit
// the authz.denied audit row when the role gate trips. The adapter
// is a free pass-through (same signature, same side-effects); see
// auth_adapters.go for the compile-time interface assertion.
func (s *server) authzAuditForCurrentRequest() authz.AuditEmitter {
	return auditorAsAuthzAuditor(s.audit)
}

// listOrgAPIKeys serves GET /v1/orgs/{slug}/keys. Returns every
// non-revoked key for the org (status IN ('active','grace'))
// ordered by created_at DESC to match the legacy listKeys
// response. The authz gate is OrgActionView — every role can
// read the keys list (the same blast radius as listing members).
//
// Errors:
//   - 403 org_role_forbidden — caller does not have View on the
//     active org (the authz gate emits authz.denied).
//   - 500 could not list keys — store failure.
func (s *server) listOrgAPIKeys(w http.ResponseWriter, r *http.Request, _ state.Account) {
	mem, ok := authz.MembershipFrom(r)
	if !ok || mem == nil {
		// Should never happen on a mounted route — loadOrg
		// stamps the membership before the handler runs. Fail
		// closed: 500 with a CodeCapacity problem so the bug
		// surfaces in logs + dashboards, not as a silent
		// passthrough.
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Active org unavailable",
			"no active org on the request; check the route table"))
		return
	}
	if p := authz.AuthorizeOrgAction(r.Context(), authz.OrgActionView, s.authzAuditForCurrentRequest()); p != nil {
		api.WriteProblem(w, p)
		return
	}
	keys, err := s.store.ListOrgAPIKeys(r.Context(), mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list keys"))
		return
	}
	out := make([]api.APIKeyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, orgAPIKeyResponse(k))
	}
	writeJSON(w, http.StatusOK, api.ListOrgAPIKeysResponse{Keys: out})
}

// createOrgAPIKey serves POST /v1/orgs/{slug}/keys. Same shape as
// the legacy createKey, but the org_id is the active-org's id
// (no OrgByPersonalAccount round-trip — LoadOrg already stamped
// the membership). The cap check is still account-wide (PR 6
// quota is per-account, not per-org).
//
// Audit: `api_key.created` only — the canonical PR 6 event. The
// legacy `key.created` event does not fire on this path (the
// legacy /v1/keys handler emits its own dual-emit set).
//
// Errors:
//   - 400 invalid scopes — NormalizeCreateKeyScopes rejected an
//     entry.
//   - 403 org_role_forbidden — caller does not have
//     org.create_api_key on the active org.
//   - 422 api_key_limit_exceeded — caller is at Plan.KeysMax.
//   - 500 capacity — store failure, key generation failure, or
//     org lookup failure.
func (s *server) createOrgAPIKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	mem, ok := authz.MembershipFrom(r)
	if !ok || mem == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Active org unavailable",
			"no active org on the request; check the route table"))
		return
	}
	var req api.CreateOrgAPIKeyRequest
	_ = decodeJSON(r, &req)
	scopes, err := api.NormalizeCreateKeyScopes(req.Scopes)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid scopes", err.Error()))
		return
	}
	// Cap check (account-wide, PR 6 quota). The org binding does
	// not change the per-account limit; a developer with keys
	// minted against two orgs still counts against the single
	// Plan.KeysMax bucket.
	limits := api.MustLimitsFor(acct.Plan)
	if cur, cerr := s.store.CountAPIKeys(r.Context(), acct.ID); cerr == nil && cur >= limits.KeysMax {
		api.WriteProblem(w, api.ErrAPIKeyLimitExceeded(limits, cur))
		return
	} else if cerr != nil {
		api.WriteProblem(w, api.ErrCapacity("could not count keys"))
		return
	}
	if p := authz.AuthorizeOrgAction(r.Context(), authz.OrgActionCreateApiKey, s.authzAuditForCurrentRequest()); p != nil {
		api.WriteProblem(w, p)
		return
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate key"))
		return
	}
	// Expiry policy mirrors the legacy handler: non-admin scopes
	// get a 365-day expires_at; admin keys default to nil
	// expiry (the legacy admin contract). The customer can
	// rotate an admin key with grace=0 to opt into finite
	// expiry.
	var expiresAt *time.Time
	if !slices.Contains(scopes, api.ScopeAdmin) {
		t := time.Now().UTC().Add(time.Duration(api.DefaultAPIKeyLifetimeDays) * 24 * time.Hour)
		expiresAt = &t
	}
	// IAM hardening mega-PR (logical change 2): stamp the
	// provenance columns on the new row. parent_key_id is nil for
	// first-mints (the FK is reserved for explicit lineage; rotation
	// already uses rotated_from_id).
	bindIP := clientIPFromRequest(r)
	bindUA := logsanitize.Field(r.UserAgent())
	k, err := s.store.CreateOrgAPIKeyWithProvenance(r.Context(), mem.OrgID, acct.ID, hash, req.Label, scopes, expiresAt, bindIP, bindUA, nil)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create key"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyKeyChanged, `{"kind":"created","org":"`+mem.OrgID+`"}`)
	s.log.Info("api key created", "key", k.ID, "account", acct.ID, "org", mem.OrgID)
	auditPayload := map[string]any{
		"key_id":     k.ID,
		"scopes":     scopes,
		"org_id":     k.OrgID,
		"created_ip": bindIP,
		"created_ua": bindUA,
	}
	if k.ExpiresAt != nil {
		auditPayload["expires_at"] = k.ExpiresAt.UTC().Format(time.RFC3339)
	}
	s.audit.Emit(r.Context(), "api_key.created", &acct.ID, auditPayload)
	resp := orgAPIKeyResponse(k)
	resp.Plaintext = plaintext
	writeJSON(w, http.StatusCreated, resp)
}

// getOrgAPIKey serves GET /v1/orgs/{slug}/keys/{id}. Returns a
// single key by (id, org_id) — cross-org reads collapse to
// ErrNotFound at the SQL level (the IDOR-safe shape documented in
// pkg/state/store.go). The authz gate is OrgActionView.
//
// Errors:
//   - 403 org_role_forbidden — role gate.
//   - 404 no such key — store returned ErrNotFound (missing OR
//     cross-org).
//   - 500 capacity — store failure.
func (s *server) getOrgAPIKey(w http.ResponseWriter, r *http.Request, _ state.Account) {
	mem, ok := authz.MembershipFrom(r)
	if !ok || mem == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Active org unavailable",
			"no active org on the request; check the route table"))
		return
	}
	if p := authz.AuthorizeOrgAction(r.Context(), authz.OrgActionView, s.authzAuditForCurrentRequest()); p != nil {
		api.WriteProblem(w, p)
		return
	}
	id := r.PathValue("id")
	k, err := s.store.GetOrgAPIKey(r.Context(), mem.OrgID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such key")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load key"))
		return
	}
	writeJSON(w, http.StatusOK, orgAPIKeyResponse(k))
}

// revokeOrgAPIKey serves DELETE /v1/orgs/{slug}/keys/{id}. The
// store's RevokeOrgAPIKey is a soft-revoke (status='revoked'; the
// row stays for audit lineage) — idempotent on repeat. The audit
// reason is "manual" so the dashboard can distinguish explicit
// revokes from rotation-driven retires ("reason: rotation") and
// lazy expiries ("reason: expired").
//
// Errors:
//   - 403 org_role_forbidden — role gate.
//   - 500 capacity — store failure.
func (s *server) revokeOrgAPIKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	mem, ok := authz.MembershipFrom(r)
	if !ok || mem == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Active org unavailable",
			"no active org on the request; check the route table"))
		return
	}
	if p := authz.AuthorizeOrgAction(r.Context(), authz.OrgActionRevokeApiKey, s.authzAuditForCurrentRequest()); p != nil {
		api.WriteProblem(w, p)
		return
	}
	id := r.PathValue("id")
	updated, err := s.store.RevokeOrgAPIKey(r.Context(), mem.OrgID, id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not revoke key"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyKeyChanged, `{"kind":"revoked","org":"`+mem.OrgID+`"}`)
	s.log.Info("api key revoked", "key", updated.ID, "account", acct.ID, "org", mem.OrgID)
	s.audit.Emit(r.Context(), "api_key.revoked", &acct.ID, map[string]any{
		"key_id": updated.ID,
		"scopes": updated.Scopes,
		"reason": "manual",
		"org_id": updated.OrgID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// rotateOrgAPIKey serves POST /v1/orgs/{slug}/keys/{id}/rotate.
// Mints a new key and demotes the old key in a single store
// transaction (issue #190 / IAM-6 / PR 6). The new key inherits
// the old key's label + scopes — the rotation is a no-op for
// the customer's CI scripts. The old key's expires_at is
// overwritten to the grace deadline (now() + graceWindow).
//
// The authz gate is OrgActionCreateApiKey (mint + rotate share
// the verb; the only difference is whether the key replaces an
// old one). Owner + admin.
//
// Decoding RotateOrgAPIKeyRequest: the only field is the
// (optional) label. The per-rotation grace window override is
// deliberately NOT on the request body — grace days are a
// per-account setting (PATCH /v1/account/keys/grace_window_days)
// and the rotation handler resolves the per-account override via
// the same s.resolveGraceWindow helper the legacy handler uses.
// PR 9 may add a per-rotation grace override if the dashboard
// asks for it.
//
// Errors:
//   - 403 org_role_forbidden — role gate.
//   - 404 no such key — store returned ErrNotFound (missing OR
//     cross-org).
//   - 404 key already revoked — store returned ErrAPIKeyRevoked;
//     a rotation of a revoked key is a 404, not idempotent.
//   - 500 capacity — store failure, key generation failure, or
//     grace window lookup failure.
func (s *server) rotateOrgAPIKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	mem, ok := authz.MembershipFrom(r)
	if !ok || mem == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Active org unavailable",
			"no active org on the request; check the route table"))
		return
	}
	var req api.RotateOrgAPIKeyRequest
	_ = decodeJSON(r, &req)
	if p := authz.AuthorizeOrgAction(r.Context(), authz.OrgActionCreateApiKey, s.authzAuditForCurrentRequest()); p != nil {
		api.WriteProblem(w, p)
		return
	}
	// Resolve grace window from the per-account override (the
	// same helper the legacy rotateKey uses). nil = plan default
	// (api.DefaultAPIKeyGraceWindowDays = 7).
	var graceWindow time.Duration
	gw, err := s.resolveGraceWindow(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve grace window"))
		return
	}
	if gw == nil {
		graceWindow = time.Duration(api.DefaultAPIKeyGraceWindowDays) * 24 * time.Hour
	} else {
		graceWindow = time.Duration(*gw) * 24 * time.Hour
	}
	// Mint the new plaintext + hash BEFORE the rotation so the
	// store op can persist the real hash. The handler is the
	// only site that ever sees the plaintext in memory.
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate key"))
		return
	}
	id := r.PathValue("id")
	// IAM hardening mega-PR (logical change 2): stamp the
	// provenance columns on the new row. parent_key_id is the
	// explicit FK to the predecessor key — distinct from the
	// rotation-internal rotated_from_id column, but for
	// rotations both point to the same predecessor.
	bindIP := clientIPFromRequest(r)
	bindUA := logsanitize.Field(r.UserAgent())
	newKey, oldKey, err := s.store.RotateOrgAPIKeyWithProvenance(r.Context(), mem.OrgID, id, hash, req.Label, graceWindow, bindIP, bindUA, nil)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such key")
			return
		}
		if errors.Is(err, state.ErrAPIKeyRevoked) {
			s.notFound(w, "key already revoked")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not rotate key"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyKeyChanged, `{"kind":"rotated","org":"`+mem.OrgID+`"}`)
	var graceWindowDays int
	if graceWindow > 0 {
		graceWindowDays = int(graceWindow / (24 * time.Hour))
	}
	// Audit payload mirrors the legacy key.rotated shape plus
	// org_id. The legacy event does NOT fire on this path (the
	// canonical /v1/orgs/{slug}/keys surface only emits the new
	// event). IAM hardening mega-PR (logical change 2): the
	// provenance columns stamp the new row at the SQL layer; the
	// audit payload mirrors the same shape so dashboards see both
	// the DB column and the audit row in lockstep.
	s.audit.Emit(r.Context(), "api_key.rotated", &acct.ID, map[string]any{
		"old_key_id":         oldKey.ID,
		"new_key_id":         newKey.ID,
		"grace_window_days":  graceWindowDays,
		"old_key_expires_at": oldKey.ExpiresAt.UTC().Format(time.RFC3339),
		"org_id":             oldKey.OrgID,
		"created_ip":         bindIP,
		"created_ua":         bindUA,
		"parent_key_id":      oldKey.ID,
	})
	s.log.Info("api key rotated",
		"old_key", oldKey.ID, "new_key", newKey.ID,
		"account", acct.ID, "org", mem.OrgID, "grace_window_days", graceWindowDays)

	resp := api.RotateOrgAPIKeyResponse{
		Key:          orgAPIKeyResponse(newKey),
		KeyPlaintext: plaintext,
		OldKeyID:     oldKey.ID,
	}
	resp.Key.RotatedFromID = oldKey.ID
	if oldKey.ExpiresAt != nil {
		resp.OldKeyExpiresAt = oldKey.ExpiresAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

// orgAPIKeyResponse projects a state.APIKey into the
// api.APIKeyResponse wire shape. The plaintext slot is left
// empty — only the create + rotate handlers have it set, and
// they set it themselves after the call.
// Used by each read-only handler (list, get) and as the seed
// for the write handlers (create/rotate) which then stamp .Plaintext.
//
// Stamp consistency: the OrgID is stamped from the row's
// OrgID (the PR 6 source of truth). The legacy /v1/keys
// handler reads the personal-org id from OrgByPersonalAccount
// and stamps it on the response — the store row's OrgID
// matches by construction, so this projection is the same
// shape across both surfaces.
func orgAPIKeyResponse(k state.APIKey) api.APIKeyResponse {
	resp := api.APIKeyResponse{
		ID:        k.ID,
		OrgID:     k.OrgID,
		Prefix:    keyPrefixFromHash(k.Hash),
		Label:     k.Label,
		Scopes:    k.Scopes,
		CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
		Status:    k.Status,
	}
	if k.LastUsedAt != nil {
		resp.LastUsedAt = k.LastUsedAt.UTC().Format(time.RFC3339)
	}
	if k.ExpiresAt != nil {
		resp.ExpiresAt = k.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if k.RevokedAt != nil {
		resp.RevokedAt = k.RevokedAt.UTC().Format(time.RFC3339)
	}
	if k.RotatedFromID != nil {
		resp.RotatedFromID = *k.RotatedFromID
	}
	return resp
}
