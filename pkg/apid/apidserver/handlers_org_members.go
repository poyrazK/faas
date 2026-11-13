// Organization member-management handlers (issue #190 / IAM-6 /
// ADR-061, PR 5).
//
// Mounted at:
//   - GET    /v1/orgs/{slug}/members                  listOrgMembers
//   - POST   /v1/orgs/{slug}/members                  inviteOrgMember
//   - PATCH  /v1/orgs/{slug}/members/{user_id}        changeOrgMemberRole
//   - DELETE /v1/orgs/{slug}/members/{user_id}        removeOrgMember
//
// All four compose s.authLimited + s.requireMFA + s.requireScope +
// s.loadOrg (mounted in cmd/apid/server.go). The slug-or-membership
// resolution already happened in the LoadOrg middleware; handlers
// read the stamped membership via authz.MembershipFrom and call
// authz.AuthorizeOrgAction with the closed OrgAction vocabulary.
//
// POST /v1/orgs/{slug}/members creates an OrgInvitation (NOT a
// direct AddOrgMember) — the handler mints a 32-byte plaintext
// token, returns it ONCE in the response, and stores only the
// SHA-256 hash. The accept flow lives in PR 8 (issue #190 PR-8)
// alongside SSO; PR 5 ships the invite-creation surface plus the
// /v1/invitations/{token} peek in handlers_org_invitations.go.

package apidserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// OrgInvitationTokenBytes is the size of the plaintext token the
// handler mints for an invitation (32 bytes → 44 chars base64url).
// Mirrors LoginTokenSize (pkg/auth/handlers_login.go) — large
// enough to make collision astronomically unlikely while keeping
// the URL-embedded token manageable.
const OrgInvitationTokenBytes = 32

// defaultOrgInvitationTtl is the lifetime of a freshly-minted
// invitation. 14 days is generous (matches the cli_auth_code
// timeline); admins can revoke earlier via RevokeOrgInvitation.
const defaultOrgInvitationTtl = 14 * 24 * time.Hour

// listOrgMembers returns the active members of the org (removed
// rows are filtered at the boundary). Each row carries the joined
// account.email so the dashboard can render "bob@acme.com" without
// a second round-trip.
//
// Mounted at GET /v1/orgs/{slug}/members.
func (s *server) listOrgMembers(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionView) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	rows, err := s.store.ListOrgMembers(r.Context(), mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"ListOrgMembers failed",
			"try again; if the problem persists, contact support"))
		return
	}
	out := api.ListMembersResponse{Members: make([]api.OrgMemberResponse, 0, len(rows))}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.RemovedAt == nil {
			ids = append(ids, row.AccountID)
		}
	}
	names := s.displayNames(r.Context(), ids)
	for _, row := range rows {
		if row.RemovedAt != nil {
			continue // Filter at the API boundary; removed rows stay in DB for audit.
		}
		// TODO(perf, PR-6+): memberToRow issues AccountByID per
		// row, which is N+1. For a 50-member org the read costs
		// 51 round-trips. Replace with a Store.ListOrgMembersWithEmail
		// that JOINs org_memberships.account_id = accounts.id in
		// a single query. Acceptable for PR 5 (small orgs); flag
		// here so PR 6 picks it up alongside the api_keys cut-over.
		member := s.memberToRow(r.Context(), row)
		member.DisplayName = names[row.AccountID]
		out.Members = append(out.Members, api.OrgMemberResponseFromRow(member))
	}
	writeJSON(w, http.StatusOK, out)
}

// inviteOrgMember mints a 32-byte plaintext token, hashes it for
// storage, and returns the plaintext ONCE. The accept flow is PR 8;
// PR 5 ships the create side + the peek side
// (handlers_org_invitations.go).
//
// Mounted at POST /v1/orgs/{slug}/members.
func (s *server) inviteOrgMember(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionInviteMembers) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	var req api.InviteMemberRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	role := state.OrgRole(strings.TrimSpace(req.Role))
	if email == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Email required", "email must be a non-empty string"))
		return
	}
	if !containsOrgMemberRoleForInvite(string(role)) {
		api.WriteProblem(w, api.ErrOrgRoleForbidden("invite members"))
		return
	}
	org, ok := s.loadMutableOrgByMembership(r.Context(), w, mem)
	if !ok {
		return
	}
	if !s.enforcePendingInvitationCap(r.Context(), w, org) {
		return
	}
	// Note: enforceMemberCap is NOT called here. Pending invitations
	// are not members — checking the member cap on the invite path
	// would block invites above `active == limit`, contradicting the
	// plan shape (members + pending invitations are two distinct
	// caps). The store-side `consumeOrgInvitation` guard remains
	// the load-bearing back-stop for accepts past the member cap;
	// the wire flow refuses at accept time with the same 403 code
	// the customer expects from "I tried to add and was over".
	token, tokenHash, mintErr := s.mintOrgInvitationToken(w)
	if mintErr {
		return
	}
	activity := newOrgAccessActivity(r, acct, org.ID, "org.invitation.created", "invitation", map[string]any{
		"role": string(role),
	})
	inv, now, ok := s.persistOrgInvitation(r.Context(), w, org, state.OrgInvitation{
		Email:              email,
		Role:               role,
		TokenHash:          tokenHash,
		InvitedByAccountID: &acct.ID,
	}, activity)
	if !ok {
		return
	}
	s.audit.Emit(r.Context(), "org.invitation.created", &acct.ID, map[string]any{
		"org_id": org.ID, "slug": org.Slug, "email": email,
		"role": string(role), "expires_at": inv.ExpiresAt,
	})
	if role == state.OrgRoleAdmin {
		s.mailOrgSecurityEvent(r.Context(), "org_admin_invited", org.ID,
			s.orgMemberLabel(r.Context(), acct.ID)+" invited "+mailpkg.MaskEmail(email)+" as an admin", acct.ID)
	}
	writeJSON(w, http.StatusCreated, api.OrgInvitationWithToken{
		OrgInvitationResponse: api.OrgInvitationResponseFromRow(invitationToRow(inv, org.Slug, now)),
		Token:                 token,
	})
}

// persistOrgInvitation creates an OrgInvitation row with the
// default TTL and stamps the supplied fields. On conflict (token-
// hash collision — astronomically unlikely at 32 bytes) writes a
// 500 Problem; on other Store failures also writes a 500 Problem.
// Returns the persisted row + now() + true; false on any error.
func (s *server) persistOrgInvitation(ctx context.Context, w http.ResponseWriter, org state.Org, base state.OrgInvitation, activity state.OrgActivity) (state.OrgInvitation, time.Time, bool) {
	now := time.Now()
	base.OrgID = org.ID
	base.ExpiresAt = now.Add(defaultOrgInvitationTtl)
	base.CreatedAt = now
	activity.Data = activityData(map[string]any{
		"role": string(base.Role), "expires_at": base.ExpiresAt.UTC().Format(time.RFC3339),
	})
	inv, err := s.createOrgInvitationWithActivity(ctx, base, activity)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			// Duplicate token_hash is astronomically unlikely
			// (32 bytes); return a 500 so the customer doesn't
			// think the invite was a no-op.
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				api.CodeCapacity, "CreateOrgInvitation collided",
				"rare token-hash collision; retry"))
		} else {
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				api.CodeCapacity, "CreateOrgInvitation failed",
				"try again; if the problem persists, contact support"))
		}
		return state.OrgInvitation{}, time.Time{}, false
	}
	return inv, now, true
}

// mintOrgInvitationToken generates 32 bytes from crypto/rand and
// returns the base64url-encoded plaintext + SHA-256 hash. On failure
// the helper writes the 500 Problem and returns mintErr=true so the
// caller short-circuits.
func (s *server) mintOrgInvitationToken(w http.ResponseWriter) (token string, tokenHash []byte, mintErr bool) {
	plaintext := make([]byte, OrgInvitationTokenBytes)
	if _, err := rand.Read(plaintext); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity, "crypto/rand failed",
			"try again; if the problem persists, contact support"))
		return "", nil, true
	}
	hash := sha256.Sum256(plaintext)
	return base64.RawURLEncoding.EncodeToString(plaintext), hash[:], false
}

// changeOrgMemberRole updates a member's role. The role-change
// action is owner-only (PR 4 closed the matrix); the handler
// refuses "owner" on a direct PATCH — transfer-ownership is the
// only path to owner.
//
// Mounted at PATCH /v1/orgs/{slug}/members/{user_id}.
func (s *server) changeOrgMemberRole(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionChangeRole) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("user_id")
	if targetID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing user_id", "user_id must be a non-empty string"))
		return
	}
	var req api.ChangeRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	role := state.OrgRole(strings.TrimSpace(req.Role))
	if !containsOrgDirectPatchRole(string(role)) {
		// owner is the only excluded role — transfer-ownership
		// is the only path to owner. Returns ErrOrgRoleForbidden
		// so the wire shape matches the matrix-checked path.
		api.WriteProblem(w, api.ErrOrgRoleForbidden("change role"))
		return
	}
	activity := newOrgAccessActivity(r, acct, mem.OrgID, "org.member.role_changed", "member", map[string]any{
		"new_role": string(role),
	})
	if err := s.updateOrgMemberRoleWithActivity(r.Context(), mem.OrgID, targetID, role, activity); err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Member not found", "the target account is not an active member of this org"))
		case errors.Is(err, state.ErrOrgLastOwner):
			api.WriteProblem(w, api.ErrOrgLastOwner())
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				api.CodeCapacity,
				"UpdateOrgMemberRole failed",
				"try again; if the problem persists, contact support"))
		}
		return
	}
	s.audit.Emit(r.Context(), "org.member.role_changed", &acct.ID, map[string]any{
		"org_id":         mem.OrgID,
		"target_account": targetID,
		"new_role":       string(role),
	})
	s.mailOrgSecurityEvent(r.Context(), "org_role_changed", mem.OrgID,
		s.orgMemberLabel(r.Context(), acct.ID)+" changed the role of "+s.orgMemberLabel(r.Context(), targetID)+" to "+string(role), targetID)
	// Re-read the row so the response carries the joined account
	// email / role pair (the store doesn't return the updated
	// row, so a single-org lookup is the only way to surface it).
	updated, ok := s.rehydrateMembership(r.Context(), w, mem, targetID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, api.OrgMemberResponseFromRow(s.memberRowWithName(r.Context(), updated)))
}

// removeOrgMember stamps removed_at on the membership row. The
// exactly-one-owner invariant lives in pkg/state::RemoveOrgMember's
// tx — the handler maps the sentinel to ErrOrgLastOwner.
//
// Mounted at DELETE /v1/orgs/{slug}/members/{user_id}.
func (s *server) removeOrgMember(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionRemoveMembers) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("user_id")
	if targetID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing user_id", "user_id must be a non-empty string"))
		return
	}
	if targetID == mem.AccountID {
		// Self-removal would leave the org without the active
		// caller. The store surfaces ErrOrgLastOwner if this is
		// the last owner; for the more common case where the
		// caller is admin, refusing at the boundary avoids the
		// awkward shape of "you removed yourself; you can no
		// longer access this org". The dashboard side UI
		// should disable the button; defence in depth here.
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Cannot remove self",
			"transfer ownership or ask another owner to remove you"))
		return
	}
	if s.refuseBillingContactRemoval(r.Context(), w, mem.OrgID, targetID) {
		return
	}
	activity := newOrgAccessActivity(r, acct, mem.OrgID, "org.member.removed", "member", nil)
	if err := s.removeOrgMemberWithActivity(r.Context(), mem.OrgID, targetID, activity); err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Member not found", "the target account is not an active member of this org"))
		case errors.Is(err, state.ErrOrgLastOwner):
			api.WriteProblem(w, api.ErrOrgLastOwner())
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				api.CodeCapacity,
				"RemoveOrgMember failed",
				"try again; if the problem persists, contact support"))
		}
		return
	}
	s.audit.Emit(r.Context(), "org.member.removed", &acct.ID, map[string]any{
		"org_id":         mem.OrgID,
		"target_account": targetID,
	})
	s.mailOrgSecurityEvent(r.Context(), "org_member_removed", mem.OrgID,
		s.orgMemberLabel(r.Context(), acct.ID)+" removed "+s.orgMemberLabel(r.Context(), targetID), targetID)
	w.WriteHeader(http.StatusNoContent)
}

// leaveOrg lets the caller end their own membership. DELETE on
// /members/{user_id} refuses self-removal because an admin removing
// themselves by accident locks them out; leaving is a separate, explicit
// verb any role may use. The owner must transfer ownership first (the
// exactly-one-owner invariant), nobody leaves their personal org, and
// the billing contact must be replaced first, as with removal.
//
// Mounted at POST /v1/orgs/{slug}/leave.
func (s *server) leaveOrg(w http.ResponseWriter, r *http.Request, acct state.Account) {
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	if problem := s.leaveOrgRefusal(r.Context(), mem); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if s.refuseBillingContactRemoval(r.Context(), w, mem.OrgID, acct.ID) {
		return
	}
	activity := newOrgAccessActivity(r, acct, mem.OrgID, "org.member.left", "member", nil)
	if err := s.removeOrgMemberWithActivity(r.Context(), mem.OrgID, acct.ID, activity); err != nil {
		switch {
		case errors.Is(err, state.ErrOrgLastOwner):
			api.WriteProblem(w, api.ErrOrgLastOwner())
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Not a member", "you are not an active member of this org"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not leave the org; try again"))
		}
		return
	}
	s.audit.Emit(r.Context(), "org.member.left", &acct.ID, map[string]any{"org_id": mem.OrgID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) leaveOrgRefusal(ctx context.Context, mem *state.OrgMembership) *api.Problem {
	if mem.Role == state.OrgRoleOwner {
		return api.NewProblem(http.StatusConflict, api.CodeOrgLastOwner,
			"Owner cannot leave", "transfer ownership to another member before leaving")
	}
	org, err := s.store.OrgByID(ctx, mem.OrgID)
	if err != nil {
		return api.ErrCapacity("could not read the org; try again")
	}
	if org.Personal {
		return api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Personal org", "you cannot leave your personal org")
	}
	return nil
}

// containsOrgMemberRoleForInvite is the membership check for the
// invite-side role. Mirrors api.AllowedOrgMemberRolesForInvite but
// accepts a flat string lookup rather than the slice index (the
// pkg/api containsString helper is unexported — this local copy
// keeps cmd/apid's vendored check explicit at the seam).
func containsOrgMemberRoleForInvite(role string) bool {
	for _, r := range api.AllowedOrgMemberRolesForInvite {
		if r == role {
			return true
		}
	}
	return false
}

// containsOrgDirectPatchRole is the membership check for the
// PATCH-side role.
func containsOrgDirectPatchRole(role string) bool {
	for _, r := range api.AllowedOrgDirectPatchRoles {
		if r == role {
			return true
		}
	}
	return false
}
