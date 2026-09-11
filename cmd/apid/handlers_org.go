// Organization CRUD handlers (issue #190 / IAM-6 / ADR-061, PR 5).
//
// Mounted at:
//   - GET    /v1/orgs                    listOrgsForCaller
//   - POST   /v1/orgs                    createSharedOrg
//   - GET    /v1/orgs/{slug}             getOrg
//   - PATCH  /v1/orgs/{slug}             patchOrg
//   - DELETE /v1/orgs/{slug}             softDeleteOrg
//
// Routes compose s.authLimited + s.requireMFA + s.requireScope(+s.loadOrg)
// in the same shape as the rest of the /v1/* surface (cmd/apid/server.go).
// GET /v1/orgs and POST /v1/orgs skip s.loadOrg because they are
// account-scoped (no active-org yet).
//
// Authz vocabulary (PR 4): every org-scoped handler composes
// authz.AuthorizeOrgAction(ctx, OrgAction*, s.audit) — the role
// matrix lives in pkg/authz/authorize.go and is the single source of
// truth for "may the active-org principal perform X?". Handlers in
// this file MUST NOT branch on mem.Role directly.

package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// listOrgsForCaller returns every org the caller has an active
// membership in (personal + shared). Account-scoped; no s.loadOrg.
// Sorted server-side by slug (matches ListOrgsForAccount's ORDER BY).
//
// Mounted at GET /v1/orgs.
func (s *server) listOrgsForCaller(w http.ResponseWriter, r *http.Request, acct state.Account) {
	orgs, err := s.store.ListOrgsForAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"ListOrgsForAccount failed",
			"try again; if the problem persists, contact support"))
		return
	}
	out := api.ListOrgsResponse{
		Orgs: make([]api.OrgResponse, 0, len(orgs)),
	}
	for _, o := range orgs {
		out.Orgs = append(out.Orgs, api.OrgResponseFromRow(orgToRow(o)))
	}
	writeJSON(w, http.StatusOK, out)
}

// createSharedOrg inserts a new shared (non-personal) org with the
// caller as the first owner. Slug validation runs at the handler so
// the wire shape stays consistent (the schema's 23514 tripwire
// would otherwise produce a raw 500).
//
// Mounted at POST /v1/orgs.
func (s *server) createSharedOrg(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateOrgRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	slug := strings.TrimSpace(req.Slug)
	name := strings.TrimSpace(req.Name)
	if reason := api.ValidateOrgSlug(slug); reason != "" {
		api.WriteProblem(w, api.ErrOrgSlugInvalid(reason))
		return
	}
	if name == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity,
			api.CodeValidation, "Name required", "name must be a non-empty string"))
		return
	}
	// Plan defaults to Free; CreateOrg stamps it. The caller seeds
	// the exactly-one-owner partial unique (zero owners at insert
	// time), so AddOrgMember cannot trip ErrOrgLastOwner.
	newOrg, err := s.store.CreateOrg(r.Context(), state.Org{Slug: slug, Name: name})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrOrgSlugTaken(slug))
		} else {
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				api.CodeCapacity, "CreateOrg failed",
				"try again; if the problem persists, contact support"))
		}
		return
	}
	// Note (IAM-6 / ADR-061 PR 2): we deliberately do NOT call
	// enforceMemberCap here — the initial-owner seed is the
	// first active membership on a brand-new org, and Free's
	// fail-closed 0/0 cap would refuse it. The cap is a
	// per-add gate for subsequent members; the personal-org
	// path (which is immutable and never reaches this handler)
	// is unaffected. The store-side cap check inside AddOrgMember
	// is the defence-in-depth back-stop — it also reads
	// 0/0 for Free but only trips when active >= limit, so
	// the initial seed (active=0) passes cleanly.
	invitedBy := acct.ID
	if err := s.store.AddOrgMember(r.Context(), newOrg.ID, acct.ID, state.OrgRoleOwner, &invitedBy); err != nil {
		if errors.Is(err, state.ErrOrgMemberCapExceeded) {
			limit := newOrg.Plan.OrgMembersMax()
			api.WriteProblem(w, api.ErrOrgMemberCapExceeded(limit, limit))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity, "AddOrgMember (initial owner) failed",
			"the org row was created but the owner membership failed to seed; contact support"))
		return
	}
	s.audit.Emit(r.Context(), "org.created", &acct.ID, map[string]any{
		"org_id": newOrg.ID, "slug": newOrg.Slug, "name": newOrg.Name,
	})
	writeJSON(w, http.StatusCreated, api.OrgResponseFromRow(orgToRow(newOrg)))
}

// getOrg returns the active org by slug. Authorise org.view first
// so a non-member sees a 403 (not a 200 with someone else's data)
// — LoadOrg has already mapped the IDOR-safe shape (404 unknown
// slug, 403 known-but-non-member), and AuthorizeOrgAction is the
// closed-vocabulary deny gate.
//
// Mounted at GET /v1/orgs/{slug}.
func (s *server) getOrg(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionView) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	org, err := s.store.OrgByID(r.Context(), mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"OrgByID failed",
			"try again; if the problem persists, contact support"))
		return
	}
	writeJSON(w, http.StatusOK, api.OrgResponseFromRow(orgToRow(org)))
}

// patchOrg updates Name. Plan is retained in the request DTO for wire
// compatibility but cannot be written locally: only a provider-confirmed
// billing flow may change an organisation's paid entitlement.
// Authz routing:
//   - name → OrgActionManageBilling (owner + billing)
//   - plan → OrgActionChangePlan (owner only)
//
// Mounted at PATCH /v1/orgs/{slug}.
func (s *server) patchOrg(w http.ResponseWriter, r *http.Request, _ state.Account) {
	var req api.PatchOrgRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.Name == nil && req.Plan == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity,
			api.CodeValidation, "No fields to update",
			"either name or plan must be supplied"))
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	if !s.authorizeOrgPatchFields(w, r, req) {
		return
	}
	if req.Plan != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired,
			api.CodePayment, "Billing confirmation required",
			"organization plans cannot be changed directly; use a provider-backed billing flow"))
		return
	}
	newName := strings.TrimSpace(*req.Name)
	if newName == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity,
			api.CodeValidation, "Name required",
			"name must be a non-empty string when supplied"))
		return
	}
	org, ok := s.loadMutableOrgByMembership(r.Context(), w, mem)
	if !ok {
		return
	}
	if err := s.store.UpdateOrgName(r.Context(), org.ID, newName); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity, "UpdateOrgName failed",
			"try again; if the problem persists, contact support"))
		return
	}
	s.audit.Emit(r.Context(), "org.updated", nil, map[string]any{
		"org_id": org.ID, "name": true, "plan": false,
	})
	updated, ok := s.rehydrateOrg(r.Context(), w, mem)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, api.OrgResponseFromRow(orgToRow(updated)))
}

// authorizeOrgPatchFields authorises the operation(s) that match
// which fields the caller is touching. Plan changes short-circuit
// on ChangePlan (owner-only); name changes fall through to
// ManageBilling (owner + billing). Both checks fire when both
// fields are present in the request body.
func (s *server) authorizeOrgPatchFields(w http.ResponseWriter, r *http.Request, req api.PatchOrgRequest) bool {
	if req.Plan != nil {
		if p := authz.AuthorizeOrgAction(r.Context(), authz.OrgActionChangePlan, s.audit); p != nil {
			api.WriteProblem(w, p)
			return false
		}
	}
	if req.Name != nil {
		if p := authz.AuthorizeOrgAction(r.Context(), authz.OrgActionManageBilling, s.audit); p != nil {
			api.WriteProblem(w, p)
			return false
		}
	}
	return true
}

// softDeleteOrg sets the deleted_pending flag. Hard delete lands
// in PR 8 (GDPR); PR 5 stamps the flag and emits the audit row.
//
// Mounted at DELETE /v1/orgs/{slug}.
func (s *server) softDeleteOrg(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionDelete) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	org, ok := s.loadMutableOrgByMembership(r.Context(), w, mem)
	if !ok {
		return
	}
	if err := s.store.SoftDeleteOrg(r.Context(), org.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"SoftDeleteOrg failed",
			"try again; if the problem persists, contact support"))
		return
	}
	s.audit.Emit(r.Context(), "org.deleted", &acct.ID, map[string]any{
		"org_id": org.ID,
		"slug":   org.Slug,
		"soft":   true,
	})
	w.WriteHeader(http.StatusNoContent)
}

// getOrgSeatUsage returns {used, limit, plan} for the active org.
// Used comes from Store.CountActiveOrgMembers (the same row the
// cap-in-tx inside ConsumeOrgInvitation reads). Limit comes from
// org.Plan.OrgMembersMax() — Free + unknown plans return 0 to match
// the fail-closed accessor. Visibility-only (no billing / meterd
// change); PR-9 ships the pricing cut-over per ADR-061 §"Out of
// scope".
//
// Mounted at GET /v1/orgs/{slug}/seat_usage. Gated by
// authz.OrgActionView (every role). The dashboard consumes the
// response directly; no further round-trip to GET /v1/orgs/{slug}
// is needed.
func (s *server) getOrgSeatUsage(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionView) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	org, err := s.store.OrgByID(r.Context(), mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"OrgByID failed",
			"try again; if the problem persists, contact support"))
		return
	}
	used, err := s.store.CountActiveOrgMembers(r.Context(), org.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity, "CountActiveOrgMembers failed",
			"try again; if the problem persists, contact support"))
		return
	}
	writeJSON(w, http.StatusOK, api.SeatUsageResponse{
		Used:  used,
		Limit: org.Plan.OrgMembersMax(),
		Plan:  string(org.Plan),
	})
}
