// Handler helpers for the /v1/orgs/{slug}/... surface
// (issue #190 / IAM-6 / ADR-061, PR 5). Extracted from the
// per-endpoint handler files (handlers_org.go,
// handlers_org_members.go, handlers_org_invitations.go) because the
// same three patterns repeat on every org-scoped mutation:
//
//   - AuthorizeOrgAction → fail-closed 403 Problem
//   - MembershipFrom → fail-closed 500 if missing
//   - post-mutation re-read so the response carries the joined row
//
// CLAUDE.md mandates handlers ≤ 50 lines. The original
// createSharedOrg (100 lines), inviteOrgMember (123 lines),
// patchOrg (65), transferOrgOwnership (63), removeOrgMember (61)
// each repeated this trio — the helpers below bring them under
// the cap. Every helper is fail-closed: on any internal
// inconsistency the helper writes a 5xx and returns false so the
// caller short-circuits with `return`.

package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// requireOrgAction is the deny-gate half of the handler prelude.
// Returns true when the caller is authorised to perform action on
// the active org (the LoadOrg middleware has stamped a Membership
// onto the principal). On deny, the helper writes the Problem
// returned by authz.AuthorizeOrgAction (the matrix-checked 403
// shape) and returns false so the caller can `return` immediately.
//
// The helper's audit-on-deny hook is wired inside
// authz.AuthorizeOrgAction itself; callers do not need to surface
// an explicit audit row for the deny path.
func (s *server) requireOrgAction(w http.ResponseWriter, r *http.Request, action authz.OrgAction) bool {
	if p := authz.AuthorizeOrgAction(r.Context(), action, s.audit); p != nil {
		api.WriteProblem(w, p)
		return false
	}
	return true
}

// requireMembership reads the LoadOrg-stamped Membership off the
// principal. Returns the membership + true when present; writes a
// fail-closed 500 Problem and returns nil + false when missing.
// The 500 is the canonical "LoadOrg wired before AuthorizeOrgAction"
// tripwire — every org-scoped route mounts s.loadOrg inside
// s.requireScope, so a nil membership means the route table is
// mis-wired (caught at review-time, not customer-time).
func (s *server) requireMembership(w http.ResponseWriter, r *http.Request) (*state.OrgMembership, bool) {
	mem, ok := authz.MembershipFrom(r)
	if !ok || mem == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"LoadOrg wired before AuthorizeOrgAction",
			"no membership on request; check the route table"))
		return nil, false
	}
	return mem, true
}

// rehydrateMembership is the post-mutation re-read used by
// transferOrgOwnership and changeOrgMemberRole so the response
// carries the joined account email + role pair (the Store
// methods don't return the updated row). Writes a 500 Problem
// on error and returns nil + false so the caller short-circuits.
func (s *server) rehydrateMembership(ctx context.Context, w http.ResponseWriter, mem *state.OrgMembership, accountID string) (state.OrgMembership, bool) {
	updated, err := s.store.OrgMemberByAccount(ctx, mem.OrgID, accountID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"OrgMemberByAccount (post-update) failed",
			"refresh and try again"))
		return state.OrgMembership{}, false
	}
	return updated, true
}

// rehydrateOrg is the post-mutation re-read used by patchOrg and
// transferOrgOwnership so the response carries the post-update
// row (Plan / Name / Status changes don't return the updated row
// from the Store methods). Writes a 500 Problem on error and
// returns the zero value + false so the caller short-circuits.
func (s *server) rehydrateOrg(ctx context.Context, w http.ResponseWriter, mem *state.OrgMembership) (state.Org, bool) {
	updated, err := s.store.OrgByID(ctx, mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"OrgByID (post-update) failed",
			"refresh and try again"))
		return state.Org{}, false
	}
	return updated, true
}

// loadMutableOrgByMembership reads the org via the membership's
// OrgID and refuses the personal-org mutation. Returns the org +
// true on success; on personal-org immutable, writes the 409
// Problem and returns zero value + false. Other Store failures
// write a 500 Problem. Used by inviteOrgMember + softDeleteOrg +
// patchOrg + transferOrgOwnership.
//
// PR-7 cross-reference: the same personal-org gate re-engages
// automatically on the convert-to-personal path described in
// ADR-061 §"Personal-org downgrade (PR-7 design, code deferred to
// PR-9)". Once a shared org has been converted (its `personal`
// flag flipped to true), this helper refuses any subsequent PATCH
// — no new code needed.
func (s *server) loadMutableOrgByMembership(ctx context.Context, w http.ResponseWriter, mem *state.OrgMembership) (state.Org, bool) {
	org, err := s.store.OrgByID(ctx, mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity, "OrgByID failed",
			"try again; if the problem persists, contact support"))
		return state.Org{}, false
	}
	if org.Personal {
		// Personal-org name + plan are denormalized from the
		// account (ADR-061 §3.2 — personal orgs are immutable).
		api.WriteProblem(w, api.ErrOrgPersonalImmutable())
		return state.Org{}, false
	}
	return org, true
}

// enforceMemberCap is the wire-side mirror of the store-side cap
// check inside ConsumeOrgInvitation. It applies the per-plan
// OrgMembersMax limit on a future direct-add (no-invite) route.
// Free + unknown plans have OrgMembersMax == 0 by plan policy —
// the abuse-floor tier cannot host shared orgs. The helper lands
// here in this PR (the populated constants per plan are the
// prerequisite) but is left intentionally unwired: today the only
// store-side paths that insert memberships are (1) AddOrgMember as
// the initial-owner seed at org creation — the store carves this
// out — and (2) ConsumeOrgInvitation on invitation accept — the
// store enforces the cap in-tx. A future direct-add route only
// needs to call `s.enforceMemberCap(...)` (read as a TODO on
// `cmd/apid/handlers_org_members.go`), the helper shape is final.
//
// PR-7 follow-up: this helper stays dead until PR-11 ships the
// direct-add route (filed as a follow-up issue). The accept path
// PR-7 added (`POST /v1/invitations/{token}/accept`) does NOT
// call enforceMemberCap because the store-side cap check inside
// ConsumeOrgInvitation is the load-bearing back-stop — see
// handlers_org_invitations.go:166-167.

// enforcePendingInvitationCap applies the per-plan
// OrgPendingInvitationsMax limit on POST /v1/orgs/{slug}/members.
// Free + unknown plans have OrgPendingInvitationsMax == 0 by plan
// policy — the abuse-floor tier cannot host shared orgs. The
// `limit <= 0` early-return keeps Free + unknown plans quiet
// (plan-policy closure, not cap arithmetic). Returns true when the
// cap allows the new invitation; on exceed, writes the 403 Problem
// and returns false. On Store failure, writes a 500 Problem and
// returns false.
func (s *server) enforcePendingInvitationCap(ctx context.Context, w http.ResponseWriter, org state.Org) bool {
	limit := org.Plan.OrgPendingInvitationsMax()
	if limit <= 0 {
		return true
	}
	pending, err := s.store.CountPendingOrgInvitations(ctx, org.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity, "CountPendingOrgInvitations failed",
			"try again; if the problem persists, contact support"))
		return false
	}
	if pending >= limit {
		api.WriteProblem(w, api.ErrOrgInvitationCapExceeded(limit, pending))
		return false
	}
	return true
}
