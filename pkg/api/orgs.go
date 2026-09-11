// Package api — org DTOs (issue #190 / IAM-6 / ADR-061, PR 5).
//
// Wire shape for /v1/orgs/{slug}/... customer surface (org CRUD, member
// management, invitations, ownership transfer). PR 4 shipped the
// authz seam + the LoadOrg middleware that resolves the X-Active-Org /
// ?org= hint to a state.OrgMembership; PR 5 wires customer-visible
// handlers on top of that seam.
//
// Naming mirrors pkg/api/alerts.go (CreateXRequest / UpdateXRequest
// pointer-everything / XResponse / XRow / XResponseFromRow) so the
// SDK generators and the dashboard treat every feature surface the
// same way.
//
// pkg/api ↔ pkg/state cycle: pkg/state imports pkg/api (for the
// api.Plan type on Account / Org). This file must NOT import
// pkg/state — the cycle is documented at memory/pkg-api-cannot-import-pkg-state
// and binds every DTO file (alerts.go, dto.go, secrets.go). State-row
// inputs cross the seam at the handler boundary (cmd/apid/org_dto.go)
// where both packages meet; the converters below accept raw strings /
// time.Time and let cmd/apid do the state.OrgRole → string mapping.

package api

import (
	"regexp"
	"strings"
	"time"
)

// OrgSlugPattern + MaxOrgSlugLen live in pkg/api/errors.go (PR 1
// shipped them alongside the ErrOrgSlugInvalid constructor). The PR
// 5 handler re-validates against the same regex at the API boundary;
// the LoadOrg middleware (pkg/authz/loadorg.go) caps at 64 chars so
// oversize values degrade to passthrough rather than a SQL CHECK 500.
//
// MinOrgSlugLen is the regex-derived lower bound (1 lead + 1..30 middle + 1 tail).
const MinOrgSlugLen = 3

// AllowedOrgRoles is the closed role vocabulary mirrored from
// pkg/state state.AllOrgRoles (owner|admin|developer|viewer|billing).
// Used by the change-role handler to validate the inbound role string
// before reaching the store (the store additionally rejects owner on
// a direct PATCH because the role-change action never promotes to
// owner — transfer-ownership is the only path to owner).
var AllowedOrgRoles = []string{
	"owner",
	"admin",
	"developer",
	"viewer",
	"billing",
}

// AllowedOrgStatuses is the closed status vocabulary. Read-only on
// the wire (PATCH /orgs/{slug} only changes name + plan — the status
// is bumped by meterd's dunning pivot in PR 7).
var AllowedOrgStatuses = []string{
	"active",
	"past_due",
	"suspended",
	"deleted_pending",
}

// AllowedOrgMemberRolesForInvite is the role set a PR-5 invite can
// carry. Excludes owner (you cannot invite someone to be an owner —
// transfer-ownership is the only path to ownership).
var AllowedOrgMemberRolesForInvite = []string{
	"admin",
	"developer",
	"viewer",
	"billing",
}

// AllowedOrgDirectPatchRoles is the role set PR-5's PATCH
// /members/{user_id} accepts. Matches AllowedOrgMemberRolesForInvite
// — both paths cannot promote to owner; transfer-ownership is the
// only path.
var AllowedOrgDirectPatchRoles = AllowedOrgMemberRolesForInvite

// CreateOrgRequest is the POST /v1/orgs body. Both slug + name are
// required. Slug is validated against OrgSlugPattern; name is
// trimmed-non-empty and capped at 256 bytes.
type CreateOrgRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// PatchOrgRequest is the PATCH /v1/orgs/{slug} body. Both fields
// are pointer-typed so the handler can distinguish "omitted" (leave
// alone) from "zero" (clear/empty). Mirrors pkg/api.UpdateAlertRuleRequest.
//
// Authz routing at the handler:
//   - name  → OrgActionManageBilling (owner + billing roles)
//   - plan  → OrgActionChangePlan (owner only), then payment_required;
//     direct entitlement writes are forbidden until the provider confirms them.
//
// Owner role writes go through ManageBilling because renaming a
// billing-relevant artefact is a billing concern, not a plan concern.
// PR 7 cut-over will re-derive the canonical org plan from the
// personal-org plan column.
type PatchOrgRequest struct {
	Name *string `json:"name,omitempty"`
	Plan *string `json:"plan,omitempty"`
}

// InviteMemberRequest is the POST /v1/orgs/{slug}/members body. The
// handler mints a 32-byte plaintext token, returns it once, and stores
// only the SHA-256 hash. Role must be a member-eligible role (see
// AllowedOrgMemberRolesForInvite) — owner is rejected because
// transfer-ownership is the only path to owner.
type InviteMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// ChangeRoleRequest is the PATCH /v1/orgs/{slug}/members/{user_id}
// body. Role must be a member-eligible role (see
// AllowedOrgDirectPatchRoles). Owner promotion is rejected at the
// handler boundary.
type ChangeMemberRoleRequest struct {
	Role string `json:"role"`
}

// ChangeRoleRequest is the historic name; renamed to
// ChangeMemberRoleRequest in PR 5 to match the spec schema
// (`PATCH /v1/orgs/{slug}/members/{user_id}` → "MemberRole").
// The alias keeps existing call-sites working.
type ChangeRoleRequest = ChangeMemberRoleRequest

// TransferOwnershipRequest is the POST /v1/orgs/{slug}/transfer_ownership
// body. NewOwnerAccountID must reference a current active member of
// the org; the previous owner becomes admin on success.
type TransferOwnershipRequest struct {
	NewOwnerAccountID string `json:"new_owner_account_id"`
}

// SeatUsageResponse is the wire shape for GET
// /v1/orgs/{slug}/seat_usage. Visibility-only (no billing / meterd
// change); the dashboard renders "X of Y seats used" from these
// three fields. PR-7 ships the count only; pricing stays out of
// scope per ADR-061 §"Out of scope" — the seat-billing cut-over
// is PR-9.
//
// Used is the live row count from
// pkg/state.Store.CountActiveOrgMembers (the same count the cap-in-tx
// inside ConsumeOrgInvitation reads). Limit is org.Plan.OrgMembersMax()
// — Free + unknown plans return 0 (the fail-closed accessor shape
// PR-2 landed) so the dashboard renders "personal org only" instead
// of "0 of 0 used". Plan is the plan string verbatim so the dashboard
// doesn't have to round-trip a separate /v1/orgs/{slug} request.
type SeatUsageResponse struct {
	Used  int    `json:"used"`
	Limit int    `json:"limit"`
	Plan  string `json:"plan"`
}

// OrgResponse is the canonical org shape on the wire. RFC3339
// timestamps (zero-time serialises as empty). Mirrors pkg/api.CronResponse
// for the timestamp posture. Used by:
//   - GET /v1/orgs/me (legacy whoamiActiveOrg handler)
//   - GET /v1/orgs (listOrgsForCaller; wrapped in ListOrgsResponse)
//   - GET /v1/orgs/{slug} (getOrg)
//   - POST /v1/orgs (createSharedOrg; wraps the new row)
//   - PATCH /v1/orgs/{slug} (patchOrg; wraps the updated row)
type OrgResponse struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Personal  bool   `json:"personal"`
	Plan      string `json:"plan"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// OrgRow is the typed counterpart of OrgResponse used at the
// pkg/api ↔ pkg/state boundary (mirrors pkg/api.AlertRuleRow).
// Fields are typed as primitives (no pkg/state import) so cmd/apid
// maps state.Org → OrgRow at the seam.
type OrgRow struct {
	ID        string
	Slug      string
	Name      string
	Personal  bool
	Plan      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OrgResponseFromRow renders a typed OrgRow as the wire-shaped
// OrgResponse. Empty-string for zero timestamps so the omitempty tag
// works (consistent with pkg/api.FormatAlertTime).
func OrgResponseFromRow(o OrgRow) OrgResponse {
	return OrgResponse{
		ID:        o.ID,
		Slug:      o.Slug,
		Name:      o.Name,
		Personal:  o.Personal,
		Plan:      o.Plan,
		Status:    o.Status,
		CreatedAt: FormatAlertTime(o.CreatedAt),
		UpdatedAt: FormatAlertTime(o.UpdatedAt),
	}
}

// ListOrgsResponse wraps the list of orgs the caller is a member of.
// The endpoint is account-scoped (no X-Active-Org required).
type OrgListResponse struct {
	Orgs []OrgResponse `json:"orgs"`
}

// ListOrgsResponse is the historic name; renamed to
// OrgListResponse in PR 5 to match the spec schema. The alias
// keeps existing call-sites working.
type ListOrgsResponse = OrgListResponse

// OrgMeResponse is the GET /v1/orgs/me wire shape (PR 4's
// whoamiActiveOrg handler). The Org field is the canonical
// OrgResponse + the caller's role on the org. When no X-Active-Org
// hint was supplied, Org is nil — pre-PR-5 routes stay account-scoped.
//
// Supersedes the PR 4 cmd/apid/handlers_org_me.go::orgMeResponse
// local type. PR 5 rewrites whoamiActiveOrg to render OrgMeResponse
// from pkg/api so the wire shape is canonical (the dashboard team's
// switcher consumes this same shape).
type OrgMeResponse struct {
	Org *OrgWithRole `json:"org"`
}

// OrgWithRole is the OrgResponse + the caller's role on the active
// org. Used only by OrgMeResponse today; owned by pkg/api so future
// endpoints (e.g. /v1/orgs/{slug}/whoami) can adopt the same shape.
type OrgWithRole struct {
	OrgResponse
	Role string `json:"role"`
}

// OrgMemberResponse is the wire shape for /v1/orgs/{slug}/members
// (the GET + the response of POST /v1/orgs/{slug}/members). The
// Email field is the joined account.email — Email is the only public
// identifier; the account id is also surfaced for non-ambiguous
// referential purposes.
type OrgMemberResponse struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	JoinedAt  string `json:"joined_at"`
}

// OrgMemberRow is the typed counterpart, joined with the account row.
type OrgMemberRow struct {
	AccountID string
	Email     string
	Role      string
	JoinedAt  time.Time
}

// OrgMemberResponseFromRow renders a typed OrgMemberRow as the
// wire-shaped OrgMemberResponse.
func OrgMemberResponseFromRow(r OrgMemberRow) OrgMemberResponse {
	return OrgMemberResponse{
		AccountID: r.AccountID,
		Email:     r.Email,
		Role:      r.Role,
		JoinedAt:  FormatAlertTime(r.JoinedAt),
	}
}

// ListMembersResponse wraps GET /v1/orgs/{slug}/members. Only active
// members (RemovedAt == nil) — removed rows are filtered at the
// handler boundary (the store returns both).
type MemberListResponse struct {
	Members []OrgMemberResponse `json:"members"`
}

// ListMembersResponse is the historic name; renamed to
// MemberListResponse in PR 5 to match the spec schema. The
// alias keeps existing call-sites working.
type ListMembersResponse = MemberListResponse

// OrgInvitationResponseFromRow renders an OrgInvitationRow as the
// wire-shaped OrgInvitationResponse. Time fields are RFC3339 strings
// via FormatAlertTime; zero-time serialises as empty.
func OrgInvitationResponseFromRow(r OrgInvitationRow) OrgInvitationResponse {
	return OrgInvitationResponse{
		ID:        r.ID,
		OrgID:     r.OrgID,
		OrgSlug:   r.OrgSlug,
		Email:     r.Email,
		Role:      r.Role,
		Status:    r.Status,
		ExpiresAt: FormatAlertTime(r.ExpiresAt),
		CreatedAt: FormatAlertTime(r.CreatedAt),
	}
}

// OrgInvitationResponse is the wire shape for /v1/orgs/{slug}/invitations
// (the GET + the response of POST). The plaintext token is NEVER
// re-served — it's returned ONCE on the create call via the
// OrgInvitationWithToken helper below.
//
// Status is the runtime-derived value (pending / consumed / revoked /
// expired) from the (consumed_at, revoked_at, expires_at) tuple. The
// store doesn't store an enum; the handler computes the value at the
// boundary.
type OrgInvitationResponse struct {
	ID        string `json:"id"`
	OrgID     string `json:"org_id"`
	OrgSlug   string `json:"org_slug"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
}

// OrgInvitationRow is the typed counterpart, joined with the org row.
type OrgInvitationRow struct {
	ID        string
	OrgID     string
	OrgSlug   string
	Email     string
	Role      string
	Status    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// OrgInvitationWithToken is the SINGLE create-call response shape
// that includes the one-time plaintext token. Future reads use
// OrgInvitationResponse alone (token is empty / not surfaced).
//
// Token is base64url-encoded (32 random bytes → 44 chars). Mirrors
// the cli_auth_codes plaintext-token pattern (pkg/auth/cli/cliauth.go).
type InvitationWithTokenResponse struct {
	OrgInvitationResponse
	Token string `json:"token"`
}

// OrgInvitationWithToken is the historic name; renamed to
// InvitationWithTokenResponse in PR 5 to match the spec schema.
// The alias keeps existing call-sites working.
type OrgInvitationWithToken = InvitationWithTokenResponse

// ListInvitationsResponse wraps GET /v1/orgs/{slug}/invitations.
// NextBefore is the cursor for the next page (matches the
// pagination contract at pkg/api/paging.go — empty means "no more
// pages"). Cursor is the last row's ID, fed back as ?before=<id>
// on the next request.
type InvitationListResponse struct {
	Invitations []OrgInvitationResponse `json:"invitations"`
	NextBefore  string                  `json:"next_before,omitempty"`
}

// ListInvitationsResponse is the historic name; renamed to
// InvitationListResponse in PR 5 to match the spec schema.
// The alias keeps existing call-sites working.
type ListInvitationsResponse = InvitationListResponse

// DeriveOrgInvitationStatus computes the runtime status from the
// (consumed_at, revoked_at, expires_at) tuple and the supplied clock.
// State machine precedence (mirrors the SQL CHECK on org_invitations):
//   - consumed   → consumed (consumed_at != nil)
//   - revoked    → revoked (covers explicit-revoke + cleanup-tick)
//   - expired    → expires_at < now && consumed_at == nil && revoked_at == nil
//   - pending    → everything else
//
// The consumed_at + revoked_at flags and the expires_at are passed as
// raw time.Time / *time.Time pairs (NOT state.OrgInvitation) so this
// helper stays free of the pkg/state import cycle.
//
// This is the canonical helper the handler uses; pkg/api/orgs_test.go
// pins all four transitions so future status logic patches have a
// single source of truth.
func DeriveOrgInvitationStatus(consumedAt, revokedAt *time.Time, expiresAt, now time.Time) string {
	switch {
	case consumedAt != nil:
		return "consumed"
	case revokedAt != nil:
		return "revoked"
	case !expiresAt.IsZero() && now.After(expiresAt):
		return "expired"
	default:
		return "pending"
	}
}

// ValidateOrgSlug returns the reason the slug is invalid (matches
// the constructor for api.ErrOrgSlugInvalid) or empty string when
// valid. Used by handlers at the API boundary AND by the
// peek-invitation handler to validate path params.
func ValidateOrgSlug(slug string) string {
	if l := len(slug); l < MinOrgSlugLen || l > MaxOrgSlugLen {
		return "slug must be between 3 and 32 characters"
	}
	if strings.ContainsAny(slug, "_") {
		return "slug may not contain underscores"
	}
	matched, err := regexp.MatchString(OrgSlugPattern, slug)
	if err != nil {
		return "invalid slug pattern"
	}
	if !matched {
		return "slug must be lowercase alphanumeric and may contain dashes"
	}
	return ""
}
