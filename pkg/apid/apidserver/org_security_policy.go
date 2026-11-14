package apidserver

// Organization security policy and ownership safety (ADR 20261113180000):
//
//   - Owners can require two-factor authentication. The org-access snapshot
//     does not admit members without it, and loadOrg answers them with
//     org_mfa_required.
//   - Transferring ownership and deleting an organization need a signed-in
//     browser: a bare API key, however it leaked, cannot hand the
//     organization to someone else or delete it.
//   - Deleting an organization and minting org API keys mail the owners.
//   - Operators can give an organization a new owner when its owner cannot
//     act.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// requireBrowserSession refuses an API-key caller of an action that needs a
// person in a signed-in browser.
func requireBrowserSession(w http.ResponseWriter, r *http.Request, what string) bool {
	if _, ok := sessionFrom(r); ok {
		return true
	}
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeBrowserSessionRequired,
		"Sign in to the dashboard",
		what+" needs a signed-in browser; API keys cannot do it. Use the Gregale dashboard."))
	return false
}

func (s *server) getOrgSecurityPolicy(w http.ResponseWriter, r *http.Request, _ state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionView) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OrgSecurityPolicyStore)
	if !ok {
		writeJSON(w, http.StatusOK, api.OrgSecurityPolicy{})
		return
	}
	policy, err := store.OrgSecurityPolicy(r.Context(), mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the organization's security policy"))
		return
	}
	writeJSON(w, http.StatusOK, orgSecurityPolicyResponse(policy))
}

func orgSecurityPolicyResponse(policy state.OrgSecurityPolicy) api.OrgSecurityPolicy {
	out := api.OrgSecurityPolicy{RequireMFA: policy.RequireMFA}
	if !policy.UpdatedAt.IsZero() {
		out.UpdatedAt = policy.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *server) putOrgSecurityPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionManageSecurity) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	var req api.OrgSecurityPolicy
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.RequireMFA && !actorAccount(r, acct).MFAEnrolled() {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeOrgMFARequired,
			"Turn on two-factor authentication first",
			"you must use two-factor authentication yourself before requiring it for the organization."))
		return
	}
	store, ok := s.store.(state.OrgSecurityPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("organization security policy is unavailable"))
		return
	}
	if err := store.SetOrgRequireMFA(r.Context(), mem.OrgID, req.RequireMFA, acct.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not save the organization's security policy"))
		return
	}
	s.audit.Emit(r.Context(), "org.security_policy.updated", &acct.ID, map[string]any{
		"org_id": mem.OrgID, "require_mfa": req.RequireMFA,
	})
	change := " stopped requiring two-factor authentication"
	if req.RequireMFA {
		change = " now requires two-factor authentication; members without it lose access until they turn it on"
	}
	s.mailOrgSecurityEvent(r.Context(), "org_security_policy", mem.OrgID,
		s.orgMemberLabel(r.Context(), acct.ID)+" changed the policy: the organization"+change, s.orgAdminIDs(r.Context(), mem.OrgID)...)
	policy, err := store.OrgSecurityPolicy(r.Context(), mem.OrgID)
	if err != nil {
		writeJSON(w, http.StatusOK, req)
		return
	}
	writeJSON(w, http.StatusOK, orgSecurityPolicyResponse(policy))
}

// orgAdminIDs lists the organization's active admins, who are told about
// changes that affect everyone's access.
func (s *server) orgAdminIDs(ctx context.Context, orgID string) []string {
	members, err := s.store.ListOrgMembers(state.WithoutOrgAccessFence(ctx), orgID)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range members {
		if m.Role == state.OrgRoleAdmin && m.RemovedAt == nil {
			out = append(out, m.AccountID)
		}
	}
	return out
}

// mailOrgCredentialCreated tells a shared organization's owners that a
// member minted or rotated an org API key: such keys can act for the
// organization long after a session ends.
func (s *server) mailOrgCredentialCreated(ctx context.Context, orgID, actorID, what, label string) {
	org, err := s.store.OrgByID(state.WithoutOrgAccessFence(ctx), orgID)
	if err != nil || org.Personal {
		return
	}
	change := s.orgMemberLabel(ctx, actorID) + " created " + strings.ToLower(what[:1]) + what[1:]
	if label != "" {
		change += " (" + label + ")"
	}
	s.mailOrgSecurityEvent(ctx, "org_api_key_created", orgID, change)
}

// operatorMakeOrgOwner gives the organization slug a new owner: the target
// account, which must be an active member with an active account. The
// previous owner, if any, becomes an admin.
func (s *server) operatorMakeOrgOwner(r *http.Request, targetID string) *api.Problem {
	slug := strings.TrimSpace(r.URL.Query().Get("org"))
	if slug == "" {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "org required", "?org=<slug> names the organization")
	}
	org, err := s.store.OrgBySlug(r.Context(), slug)
	if err != nil {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Organization not found", "no organization with that slug")
	}
	assigner, ok := s.store.(state.OrgOwnerAssigner)
	if !ok {
		return api.ErrCapacity("owner assignment is unavailable")
	}
	previous, err := assigner.AssignOrgOwner(r.Context(), org.ID, targetID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Member not found",
			"the account is not an active member of that organization (personal organizations keep their owner)")
	case errors.Is(err, state.ErrOrgOwnerInactive):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Account not active",
			"only an active account can own an organization")
	case err != nil:
		return api.ErrCapacity("could not assign the organization's owner")
	}
	s.audit.Emit(r.Context(), "org.ownership_assigned_by_operator", &targetID, map[string]any{
		"org_id": org.ID, "previous_owner": previous, "new_owner": targetID,
	})
	s.mailOrgSecurityEvent(r.Context(), "org_ownership_transferred", org.ID,
		"Gregale support made "+s.orgMemberLabel(r.Context(), targetID)+" the owner", previous)
	return nil
}
