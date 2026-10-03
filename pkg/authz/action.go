// Package authz holds the organization-scoped authorization surface for
// the one-box FaaS platform. It implements the authz half of the
// auth/authz split that ADR-046 established for apid's bearer + cookie
// chain; pkg/auth/middleware answers "who is this caller?" and
// pkg/authz answers "what may they do in this org?".
//
// The seam contract referenced from pkg/state/store.go::CreateOrg's TODO
// comment — "Every org/membership/invitation accessor must be served
// through pkg/authz.RequireOrgAction(action)" — is the load-bearing
// reason this package exists. PR 4 (issue #190 / IAM-6 / ADR-061)
// ships the vocabulary + the role matrix + the LoadOrg middleware; PR
// 5+ wires every org handler through AuthorizeOrgAction.
//
// The five-role vocabulary (owner|admin|developer|viewer|billing)
// matches the SQL CHECK in migrations/00099_orgs_memberships_invitations.sql.
// The action vocabulary mirrors api.Scope* but is org-scoped, not
// api-key-scope-typed — a caller's API key scopes and their org role
// compose orthogonally.
package authz

import "github.com/onebox-faas/faas/pkg/state"

// OrgAction is one of the twelve role-checked verbs the org surface
// understands. The vocabulary is closed (issue #190 / IAM-6 / ADR-061):
// PR 5/6 add handlers that compose these constants — they do NOT add
// new ad-hoc verbs.
type OrgAction string

const (
	// OrgActionView is the read-everything baseline every role has.
	// Composed by GET /v1/orgs/{slug}, /v1/orgs/{slug}/members,
	// /v1/orgs/{slug}/invitations, etc.
	OrgActionView OrgAction = "org.view"

	// OrgActionManageMembers covers adding members directly (admin
	// path — no invitation token involved) and removing members.
	// The accept-invitation path uses OrgActionInviteMembers (the
	// inviter gates, the invitee is always implicit).
	OrgActionManageMembers OrgAction = "org.manage_members"

	// OrgActionInviteMembers is the "create an invitation token"
	// action. Distinct from ManageMembers because some customers
	// will want to allow billing-only seats to issue invites
	// without granting full member-management.
	OrgActionInviteMembers OrgAction = "org.invite_members"

	// OrgActionRemoveMembers is removing an existing member. Owner
	// only — admin cannot demote or remove (the exactly-one-owner
	// rule lives in pkg/state::RemoveOrgMember's tx; this is the
	// orthogonal role-gate check).
	OrgActionRemoveMembers OrgAction = "org.remove_members"

	// OrgActionChangeRole is PATCH /v1/orgs/{slug}/members/{user_id}.
	// Owner only — the role-change action is the one action that
	// can directly affect ownership invariants, so it stays
	// owner-only even for admins.
	OrgActionChangeRole OrgAction = "org.change_role"

	// OrgActionTransferOwnership is the vacate-the-org action.
	// Owner only; the new owner must already be a member.
	OrgActionTransferOwnership OrgAction = "org.transfer_ownership"

	// OrgActionManageBilling covers Stripe customer mapping,
	// payment-method management, and invoice reads. Billing + Owner.
	OrgActionManageBilling OrgAction = "org.manage_billing"

	// OrgActionChangePlan covers plan upgrades/downgrades. Owner
	// only — a billing seat can view invoices but cannot change
	// the plan that drives them.
	OrgActionChangePlan OrgAction = "org.change_plan"

	// OrgActionDelete is DELETE /v1/orgs/{slug} (the soft-delete
	// path — sets deleted_pending; PR 8 wires the actual purge).
	OrgActionDelete OrgAction = "org.delete"

	// OrgActionCreateApiKey is POST /v1/orgs/{slug}/keys +
	// POST /v1/orgs/{slug}/keys/{id}/rotate (mint + rotate share
	// the verb — both gate key-material issuance, the only
	// difference is whether the key replaces an old one). Owner
	// + admin. PR 6 ships this verb.
	OrgActionCreateApiKey OrgAction = "org.create_api_key"

	// OrgActionRevokeApiKey is DELETE /v1/orgs/{slug}/keys/{id}.
	// Owner + admin. PR 6 ships this verb.
	OrgActionRevokeApiKey OrgAction = "org.revoke_api_key"

	// OrgActionCreateApp gates provisioning an app in the active org.
	// Owners, admins, and developers may create; viewer and billing roles
	// cannot provision infrastructure.
	OrgActionCreateApp OrgAction = "org.create_app"
)

// AllOrgActions is the closed vocabulary in iteration order. Used by
// authorize_test.go to assert the matrix is fully populated (a missing
// cell trips the test rather than silently allow everything).
var AllOrgActions = []OrgAction{
	OrgActionView,
	OrgActionManageMembers,
	OrgActionInviteMembers,
	OrgActionRemoveMembers,
	OrgActionChangeRole,
	OrgActionTransferOwnership,
	OrgActionManageBilling,
	OrgActionChangePlan,
	OrgActionDelete,
	OrgActionCreateApiKey,
	OrgActionRevokeApiKey,
	OrgActionCreateApp,
}

// AllOrgRoles is the closed role vocabulary in priority order. The
// priority is used to break ties when multiple roles would apply
// (they shouldn't — each org_memberships row has exactly one role —
// but the iteration order is load-bearing for the matrix dump tests).
var AllOrgRoles = []state.OrgRole{
	state.OrgRoleOwner,
	state.OrgRoleAdmin,
	state.OrgRoleDeveloper,
	state.OrgRoleViewer,
	state.OrgRoleBilling,
}
