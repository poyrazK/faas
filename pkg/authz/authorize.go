package authz

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// allowRoleMatrix maps every OrgAction to the set of roles that may
// perform it. The table is the single source of truth — handlers MUST
// route through AuthorizeOrgAction (or one of the RequireRole helpers
// below) instead of branching on role directly. PR 5 will enforce this
// in code review for every org handler.
//
// The matrix is built once at package init via init() below; the
// compile-time assertion in authorize_test.go pins every
// (action, role) cell. A missing cell returns false (deny), not panic
// — fail closed.
var allowRoleMatrix = map[OrgAction]map[state.OrgRole]bool{
	OrgActionSetApplicationStandardLocalIntent: {
		state.OrgRoleOwner:     true,
		state.OrgRoleAdmin:     true,
		state.OrgRoleDeveloper: true,
	},
	OrgActionViewApplicationStandards: {
		state.OrgRoleOwner:     true,
		state.OrgRoleAdmin:     true,
		state.OrgRoleDeveloper: true,
		state.OrgRoleViewer:    true,
		state.OrgRoleBilling:   true,
	},
	OrgActionManageApplicationStandards: {
		state.OrgRoleOwner: true,
		state.OrgRoleAdmin: true,
	},
	OrgActionApproveApplicationStandards: {
		state.OrgRoleOwner: true,
		state.OrgRoleAdmin: true,
	},
	OrgActionView: {
		state.OrgRoleOwner:     true,
		state.OrgRoleAdmin:     true,
		state.OrgRoleDeveloper: true,
		state.OrgRoleViewer:    true,
		state.OrgRoleBilling:   true,
	},
	OrgActionManageMembers: {
		state.OrgRoleOwner: true,
		state.OrgRoleAdmin: true,
	},
	OrgActionInviteMembers: {
		state.OrgRoleOwner: true,
		state.OrgRoleAdmin: true,
	},
	OrgActionRemoveMembers: {
		// Per ADR-061, removal is owner-only because removing an
		// admin or developer affects the org's ability to operate
		// — even admins cannot unilaterally remove peers. The
		// exactly-one-owner invariant is enforced separately in
		// pkg/state::RemoveOrgMember's tx.
		state.OrgRoleOwner: true,
	},
	OrgActionChangeRole: {
		// The role-change action is the one action that can
		// directly affect ownership invariants (a malicious admin
		// could promote themselves to owner), so it stays owner-
		// only.
		state.OrgRoleOwner: true,
	},
	OrgActionTransferOwnership: {
		state.OrgRoleOwner: true,
	},
	OrgActionManageBilling: {
		state.OrgRoleOwner:   true,
		state.OrgRoleBilling: true,
	},
	OrgActionChangePlan: {
		state.OrgRoleOwner: true,
	},
	OrgActionDelete: {
		state.OrgRoleOwner: true,
	},
	// PR 6 (issue #190 / IAM-6) — org-bound API key mint/rotate
	// and revoke. Owner + admin; mirrors the org.manage_members
	// pattern (the existing precedent that "owner + admin =
	// day-to-day ops, owner only = invariant-affecting"). Mint
	// is the one action that hands out credential material; it
	// is the most security-sensitive of the org actions a
	// developer-role caller could otherwise attempt, so it's
	// admin+owner only (developer/viewer/billing are
	// intentionally absent).
	OrgActionCreateApiKey: {
		state.OrgRoleOwner: true,
		state.OrgRoleAdmin: true,
	},
	// Revoke mirrors create — same role set, same rationale
	// (revoking a contractor's key is the same blast radius as
	// minting one). Locked to owner + admin so an attacker who
	// somehow got a developer's bearer can't lock the team
	// out of CI by deleting every active key.
	OrgActionRevokeApiKey: {
		state.OrgRoleOwner: true,
		state.OrgRoleAdmin: true,
	},
	// Creating infrastructure is day-to-day workspace work for operators;
	// viewer and billing memberships intentionally do not provision apps.
	OrgActionCreateApp: {
		state.OrgRoleOwner:     true,
		state.OrgRoleAdmin:     true,
		state.OrgRoleDeveloper: true,
	},
}

// AuthorizeOrgAction returns nil if the active-org principal on ctx
// has the role required for `action`, or a typed *api.Problem
// otherwise. The 403 problem uses api.CodeOrgRoleForbidden (PR 1
// of issue #190 / IAM-6 / ADR-061) with the action and the caller's
// role in the detail string.
//
// Callers that don't compose LoadOrg (pre-PR-5 routes) get a 403 with
// the action and a "<no active org>" detail — those routes should
// reach AuthorizeOrgAction only when they are explicitly org-scoped
// (PR 5 will own that gate).
//
// If `audit` is non-nil, denials emit an "authz.denied" audit row
// keyed by the principal's account id (matching the key.expired /
// key.auth_rejected_revoked pattern in pkg/auth/middleware).
func AuthorizeOrgAction(ctx context.Context, action OrgAction, audit AuditEmitter) *api.Problem {
	acct, _, mem, ok := principalFromCtx(ctx)
	if !ok {
		// Fail closed. Should never happen on a wired route —
		// RequireSession runs before LoadOrg.
		p := api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"AuthorizeOrgAction wired before RequireSession",
			"no principal on context; check the route table")
		emitAudit(ctx, audit, acct, action.String(), p)
		return p
	}
	if mem == nil {
		// The route was reached but the caller didn't pass an
		// X-Active-Org / ?org= hint. Treat as 403, not 404, so
		// the wire shape is consistent across deny paths.
		p := api.ErrOrgRoleForbidden(action.String())
		emitAudit(ctx, audit, acct, action.String(), p)
		return p
	}
	allowed, known := allowRoleMatrix[action]
	if !known {
		// Defensive: every OrgAction should be in the matrix. A
		// missing entry means a developer added an OrgAction
		// constant without updating the matrix — fail closed so
		// the omission surfaces in tests rather than silently
		// allowing the action.
		p := api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Authorization table missing entry",
			"action "+action.String()+" is not in the allowRoleMatrix; update pkg/authz/authorize.go")
		emitAudit(ctx, audit, acct, action.String(), p)
		return p
	}
	if allowed[mem.Role] {
		return nil
	}
	p := api.ErrOrgRoleForbidden(action.String())
	emitAudit(ctx, audit, acct, action.String(), p)
	return p
}

// emitAudit is a tiny helper that no-ops when audit is nil so callers
// don't need to nil-check at every site. Audit emission is opt-in per
// call to keep the cost off the hot path until PR 5+ builds the
// audit-row indexing it needs.
//
// The action parameter is a string (not OrgAction) so callers can emit
// resolution-failure events like "org.load.not_found" alongside
// authorization-failure events like "org.delete" without bypassing
// the OrgAction closed-vocabulary discipline: deny sites convert
// via OrgAction.String; load sites pass their own namespaced strings.
func emitAudit(ctx context.Context, audit AuditEmitter, acct state.Account, action string, p *api.Problem) {
	if audit == nil {
		return
	}
	detail := ""
	if p != nil {
		detail = p.Detail
	}
	audit.Emit(ctx, "authz.denied", nil, map[string]any{
		"action":      action,
		"account_id":  acct.ID,
		"status":      p.Code,
		"http_status": p.Status,
		"detail":      detail,
	})
}

// AuditEmitter is the minimal interface pkg/authz needs from the
// audit package. Avoids importing pkg/audit directly so pkg/authz
// stays testable without booting the audit daemon. The real
// implementation is pkg/audit.EmitRow (or whatever the audit
// package exposes — wired in cmd/apid/main.go).
//
// If you change this signature, also update the audit-fake in
// authorize_test.go.
type AuditEmitter interface {
	Emit(ctx context.Context, event string, accountID *string, fields map[string]any)
}

// String renders OrgAction for log/problem-detail purposes. Stable
// across renames (the wire form is the action's declared value).
func (a OrgAction) String() string {
	return string(a)
}

// RequireRole is the package-level helper for handlers that want to
// short-circuit before doing any work. It mirrors pkg/auth/middleware's
// RequireScope shape:
//
//	if p := authz.AuthorizeOrgAction(ctx, authz.OrgActionManageMembers, s.audit); p != nil {
//	    api.WriteProblem(w, p)
//	    return
//	}
//
// is the canonical call shape. We do NOT export a separate
// RequireRole wrapper because the return-and-write idiom is one line
// and the explicit return value makes the audit-emission contract
// visible to readers. PR 5 will land a small helper for the SSE
// paths that need to flush before the body starts (those compose
// the same call with a custom writer).
