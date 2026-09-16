// Organization invitation + ownership-transfer handlers
// (issue #190 / IAM-6 / ADR-061, PR 5 + PR 7).
//
// Mounted at:
//   - GET    /v1/invitations/{token}                       peekInvitation
//   - POST   /v1/invitations/{token}/accept                acceptInvitation
//   - DELETE /v1/orgs/{slug}/invitations/{invitation_id}   revokeInvitation
//   - POST   /v1/orgs/{slug}/transfer_ownership            transferOrgOwnership
//
// The invitation-create handler (POST /v1/orgs/{slug}/members) lives
// in handlers_org_members.go (create-only — returns the plaintext
// token ONCE). PR 5 shipped the peek + transfer surfaces; PR 7
// closes the remaining customer-facing flows:
//   - acceptInvitation consumes the token via Store.ConsumeOrgInvitation
//     and emits the two audit kinds the dashboard needs (the member-
//     side row plus the invitation-side row, both at the same call site).
//   - revokeInvitation stamps revoked_at via Store.RevokeOrgInvitation
//     and emits org.invitation.revoked. Gated by OrgActionInviteMembers
//     (owner + admin), symmetric with the create-invite path.
// PR 8 (SSO + GDPR bundle) reconsiders step-up at accept time.

package main

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

// peekInvitation is the read-only GET /v1/invitations/{token}
// surface. It does NOT consume the token — the accept path is PR 8.
//
// Mounted without s.loadOrg: the invitee doesn't have an
// X-Active-Org yet (the invitation IS how they get an active-org).
// The token-bearing query is the authentication seam. The route is
// still s.auth-bounded so the caller has a valid session or API
// key, but no org-scoped RBAC fires (the principal doesn't yet
// belong to the target org).
//
// Mounted at GET /v1/invitations/{token}.
func (s *server) peekInvitation(w http.ResponseWriter, r *http.Request, _ state.Account) {
	token := r.PathValue("token")
	if token == "" {
		api.WriteProblem(w, api.ErrOrgInvitationInvalid())
		return
	}
	plaintext, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		// Malformed token → 410 (don't leak whether a token was
		// ever minted; same posture as the LoadOrg 404 path on
		// unknown slugs).
		api.WriteProblem(w, api.ErrOrgInvitationInvalid())
		return
	}
	hash := sha256.Sum256(plaintext)
	inv, err := s.store.OrgInvitationByTokenHash(r.Context(), hash[:])
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrOrgInvitationInvalid())
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"OrgInvitationByTokenHash failed",
			"try again; if the problem persists, contact support"))
		return
	}
	org, err := s.store.OrgByID(r.Context(), inv.OrgID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"OrgByID failed",
			"the invitation's org row vanished; contact support"))
		return
	}
	now := time.Now()
	status := api.DeriveOrgInvitationStatus(inv.ConsumedAt, inv.RevokedAt, inv.ExpiresAt, now)
	if status == "consumed" || status == "revoked" || status == "expired" {
		// Collapse every terminal-state token (consumed /
		// revoked / expired) onto a single ErrOrgInvitationInvalid
		// so an attacker who has a leaked token cannot enumerate
		// which rows are still live by the code returned. The
		// Expired constructor is reserved for the PR 8 accept
		// flow where the caller is the legitimate invitee and
		// needs to know "this token is past its TTL" so the
		// dashboard can render a "request a new invite" CTA.
		api.WriteProblem(w, api.ErrOrgInvitationInvalid())
		return
	}
	row := invitationToRow(inv, org.Slug, now)
	writeJSON(w, http.StatusOK, api.OrgInvitationResponseFromRow(row))
}

// transferOrgOwnership atomically promotes the target account to
// owner and demotes the caller to admin via Store.TransferOrgOwnership.
//
// Mounted at POST /v1/orgs/{slug}/transfer_ownership.
func (s *server) transferOrgOwnership(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionTransferOwnership) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	var req api.TransferOwnershipRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	newOwnerID := strings.TrimSpace(req.NewOwnerAccountID)
	if newOwnerID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing new_owner_account_id", "new_owner_account_id must be a non-empty string"))
		return
	}
	if newOwnerID == mem.AccountID {
		// Self-transfer is the canonical "already the owner"
		// case; refuse explicitly so the wire shape stays
		// consistent (the store would otherwise return
		// ErrOrgLastOwner).
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Already owner", "the caller is already the owner; no transfer needed"))
		return
	}
	if err := s.store.TransferOrgOwnership(r.Context(), mem.OrgID, mem.AccountID, newOwnerID); err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Member not found", "the new owner is not an active member of this org"))
		case errors.Is(err, state.ErrOrgLastOwner):
			api.WriteProblem(w, api.ErrOrgLastOwner())
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				api.CodeCapacity,
				"TransferOrgOwnership failed",
				"try again; if the problem persists, contact support"))
		}
		return
	}
	s.audit.Emit(r.Context(), "org.ownership_transferred", &acct.ID, map[string]any{
		"org_id":       mem.OrgID,
		"from_account": acct.ID,
		"to_account":   newOwnerID,
	})
	updated, ok := s.rehydrateOrg(r.Context(), w, mem)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, api.OrgResponseFromRow(orgToRow(updated)))
}

// acceptInvitation consumes an invitation token, inserts the
// membership via Store.ConsumeOrgInvitation, and emits both the
// member-side and the invitation-side audit rows. The store-side
// cap-in-tx check is the load-bearing back-stop (mirrors the
// invite-side posture documented at
// handlers_org_members.go:127-134) — this handler does NOT call
// enforceMemberCap.
//
// Mounted at POST /v1/invitations/{token}/accept. Auth chain is
// s.authLimited → s.requireMFA → s.requireStepUp(5m) (the
// 5-minute TTL + audit kind come from pkg/auth.Middleware
// .RequireStepUp at middleware.go:802-873; PR-8 wires the chain,
// ADR-077 documents the rationale). No s.loadOrg — the invitee
// has no X-Active-Org yet (the invitation IS how they get one).
// Bearer-key principals skip the step-up gate (an API key is
// step-up-equivalent proof).
func (s *server) acceptInvitation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	token := r.PathValue("token")
	if token == "" {
		api.WriteProblem(w, api.ErrOrgInvitationInvalid())
		return
	}
	plaintext, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		api.WriteProblem(w, api.ErrOrgInvitationInvalid())
		return
	}
	hash := sha256.Sum256(plaintext)

	// Email match + state + cap are all enforced inside
	// ConsumeOrgInvitation's tx. We surface the typed sentinels
	// to the same 410/403/409 the dashboard expects.
	membership, inv, err := s.store.ConsumeOrgInvitation(r.Context(), hash[:], acct)
	switch {
	case errors.Is(err, state.ErrOrgInvitationInvalid), errors.Is(err, state.ErrOrgInvitationExpired):
		api.WriteProblem(w, api.ErrOrgInvitationInvalid())
		return
	case errors.Is(err, state.ErrOrgAlreadyMember):
		// Both store impls return zero OrgMembership{} on this
		// sentinel (the existing membership is discoverable but
		// the error itself carries no role). Surface the actual
		// role via a fresh lookup: re-fetch the invitation for
		// OrgID (the returned inv is also zero-valued here), then
		// OrgMemberByAccount. Best-effort — on lookup failure
		// fall back to "" (the role is informational, not
		// load-bearing). Mirrors the secretbox SealBytes posture:
		// surface what we know, never guess.
		existingRole := ""
		if reFetched, lookupErr := s.store.OrgInvitationByTokenHash(r.Context(), hash[:]); lookupErr == nil {
			if existing, mErr := s.store.OrgMemberByAccount(r.Context(), reFetched.OrgID, acct.ID); mErr == nil {
				existingRole = string(existing.Role)
			}
		}
		api.WriteProblem(w, api.ErrOrgAlreadyMember(existingRole))
		return
	case errors.Is(err, state.ErrOrgMemberCapExceeded):
		// The store reads the live cap; the dashboard can render
		// "the plan caps this org at N members" via the seat_usage
		// endpoint if the customer needs the numeric details.
		api.WriteProblem(w, api.ErrOrgMemberCapExceeded(0, 0))
		return
	case err != nil:
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity, "ConsumeOrgInvitation failed",
			"try again; if the problem persists, contact support"))
		return
	}

	// Two audit rows from one customer action: the invitation-side
	// record (who/when/role) and the member-side record (the
	// resulting membership). Both are emitted post-mutation per
	// the ADR-035 best-effort contract; failure to emit does not
	// roll back the membership insert.
	s.audit.Emit(r.Context(), "org.invitation.accepted", &acct.ID, map[string]any{
		"org_id":     inv.OrgID,
		"email":      inv.Email,
		"role":       string(inv.Role),
		"invitation": inv.ID,
	})
	s.audit.Emit(r.Context(), "org.member.added", &acct.ID, map[string]any{
		"org_id":     inv.OrgID,
		"email":      inv.Email,
		"role":       string(membership.Role),
		"invitation": inv.ID,
	})

	writeJSON(w, http.StatusOK, api.OrgMemberResponseFromRow(s.memberToRow(r.Context(), membership)))
}

// revokeInvitation stamps revoked_at on a still-pending invitation
// and emits org.invitation.revoked. Owner + admin only
// (authz.OrgActionInviteMembers — symmetric with inviteOrgMember).
// The store enforces ErrOrgInvitationInvalid when the row is
// already consumed / revoked / unknown.
//
// Mounted at DELETE /v1/orgs/{slug}/invitations/{invitation_id}. The stable
// row ID comes from the invitation list; plaintext tokens remain confined to
// peek and accept flows.
func (s *server) revokeInvitation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionInviteMembers) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	invitationID := r.PathValue("invitation_id")
	if _, err := uuid.Parse(invitationID); err != nil {
		api.WriteProblem(w, api.ErrOrgInvitationInvalid())
		return
	}

	if err := s.store.RevokeOrgInvitation(r.Context(), mem.OrgID, invitationID, acct.ID); err != nil {
		switch {
		case errors.Is(err, state.ErrOrgInvitationInvalid), errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.ErrOrgInvitationInvalid())
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				api.CodeCapacity, "RevokeOrgInvitation failed",
				"try again; if the problem persists, contact support"))
		}
		return
	}
	s.audit.Emit(r.Context(), "org.invitation.revoked", &acct.ID, map[string]any{
		"org_id":        mem.OrgID,
		"invitation_id": invitationID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// listOrgInvitations renders the org-scoped invitation list at
// GET /v1/orgs/{slug}/invitations. Cursor-paginated via
// ?before=<cursor>&limit=<n> (default 25, max 100 — matches the
// pattern at handlers_account_scoped.go:54-82). Every role can
// view (authz.OrgActionView); the same authz gate as
// listOrgMembers so the dashboard's "Members" + "Pending
// invitations" tables share the access model.
//
// Mounted at GET /v1/orgs/{slug}/invitations. PR-8 closes the
// dashboard-render gap from PR-5 (the store method existed but
// the handler never shipped). Emits one org.invitation.viewed
// audit row per render — cheap (per-render, not per-row) and
// documents "an admin looked at the pending invites" for the
// compliance audit (ADR-035 §"Kind taxonomy" is success-only;
// this row fires only when the page returns 200).
//
// PR-9: the wire cursor is now a compound (created_at, id)
// key encoded by pkg/cursor (base64url(JSON)). The store
// decodes internally; the handler emits the compound cursor
// from the last row's (created_at, id) and maps
// state.ErrInvalidCursor to a 400 validation_failed with the
// stable code `invalid_cursor` (the v1 cursor silently returned
// 0 rows on malformed input — DO NOT regress).
func (s *server) listOrgInvitations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionView) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), 25, 100, "invitations")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	before := r.URL.Query().Get("before")
	rows, err := s.store.ListOrgInvitationsForOrgPage(r.Context(), mem.OrgID, limit, before)
	if err != nil {
		if errors.Is(err, state.ErrInvalidCursor) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest,
				"invalid_cursor",
				"Invalid cursor",
				"the `before` query parameter is not a valid cursor; pass `next_before` from the prior response unchanged, or omit it for the first page"))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"ListOrgInvitationsForOrgPage failed",
			"try again; if the problem persists, contact support"))
		return
	}
	// Resolve the org slug for the translator. The store joins
	// org_invitations.org_id → orgs.id, but mem.OrgMembership
	// doesn't carry the slug (PR-5 left it off; the LoadOrg
	// middleware resolves it from the X-Active-Org header). One
	// extra round-trip per render is fine for the dashboard's
	// "Pending invitations" panel (small N).
	org, err := s.store.OrgByID(r.Context(), mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"OrgByID failed",
			"try again; if the problem persists, contact support"))
		return
	}
	now := time.Now()
	out := api.InvitationListResponse{
		Invitations: make([]api.OrgInvitationResponse, 0, len(rows)),
	}
	for _, inv := range rows {
		out.Invitations = append(out.Invitations,
			api.OrgInvitationResponseFromRow(invitationToRow(inv, org.Slug, now)))
	}
	// PR-9: emit the compound cursor (created_at, id) of the last
	// row. The page is "full" when limit rows were returned AND
	// at least one row exists — both conditions match the v1
	// gate len(rows) == limit && len(rows) > 0 so the next-page
	// termination semantics are unchanged.
	if len(rows) == limit && len(rows) > 0 {
		last := rows[len(rows)-1]
		out.NextBefore = cursor.Encode(cursor.Key{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
	}
	s.audit.Emit(r.Context(), "org.invitation.viewed", &acct.ID, map[string]any{
		"org_id":        mem.OrgID,
		"row_count":     len(out.Invitations),
		"had_next_page": out.NextBefore != "",
	})
	writeJSON(w, http.StatusOK, out)
}
