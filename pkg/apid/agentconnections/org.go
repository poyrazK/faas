package agentconnections

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// An organization's agents and its agent policy (ADR-20261101225122). Owners
// and admins manage both; every member reads the policy.

// OrgDeps are the apid services the organization handlers use.
type OrgDeps struct {
	Deps
	// AuthzAudit records refused role checks.
	AuthzAudit func() authz.AuditEmitter
	// RevokeOrg is apid's organization key revocation: it records the
	// organization's activity and invalidates authentication caches.
	RevokeOrg func(r *http.Request, acct state.Account, orgID, keyID, reason string) error
	// RecordPolicy records a policy change in the organization's activity.
	RecordPolicy func(r *http.Request, acct state.Account, policy state.OrgAgentPolicy, revoked int)
}

// activeOrg returns the request's organization once the caller's role
// allows action, or writes the problem.
func (d OrgDeps) activeOrg(w http.ResponseWriter, r *http.Request, action authz.OrgAction) (string, bool) {
	mem, ok := authz.MembershipFrom(r)
	if !ok || mem == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeCapacity, "Active org unavailable", "no active org on the request; check the route table"))
		return "", false
	}
	if p := authz.AuthorizeOrgAction(r.Context(), action, d.AuthzAudit()); p != nil {
		api.WriteProblem(w, p)
		return "", false
	}
	return mem.OrgID, true
}

// orgConnected returns the organization's usable agent keys, newest first.
func orgConnected(ctx context.Context, store state.Store, orgID string) ([]state.APIKey, error) {
	keys, err := store.ListOrgAPIKeys(ctx, orgID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := keys[:0:0]
	for _, k := range keys {
		if Kind(k) != "" && usable(k, now) {
			out = append(out, k)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// OrgList handles GET /v1/orgs/{slug}/agent-connections.
func OrgList(d OrgDeps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		orgID, ok := d.activeOrg(w, r, authz.OrgActionRevokeApiKey)
		if !ok {
			return
		}
		keys, err := orgConnected(r.Context(), d.Store, orgID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list agent connections"))
			return
		}
		clients, members := d.orgDetails(r.Context(), keys)
		prefixes := d.Prefixes(r.Context(), keys)
		out := api.AgentConnectionsResponse{Items: make([]api.AgentConnectionResponse, 0, len(keys))}
		for _, k := range keys {
			item := response(k, clients[k.ID], prefixes[k.ID])
			item.Member = members[k.AccountID]
			item.Projects = d.projects(r.Context(), k.ID)
			item.Approvals = d.approvals(r.Context(), k)
			out.Items = append(out.Items, item)
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

// orgDetails names each key's client and each member who connected one.
func (d OrgDeps) orgDetails(ctx context.Context, keys []state.APIKey) (map[string]state.AgentClient, map[string]string) {
	clients, members := map[string]state.AgentClient{}, map[string]string{}
	names, _ := d.Store.(state.AgentClientStore)
	for _, k := range keys {
		if _, seen := members[k.AccountID]; seen {
			continue
		}
		members[k.AccountID] = ""
		if acct, err := d.Store.AccountByID(ctx, k.AccountID); err == nil {
			members[k.AccountID] = acct.Email
		}
		if names == nil {
			continue
		}
		named, err := names.APIKeyAgentClients(ctx, k.AccountID)
		if err != nil {
			continue
		}
		for id, client := range named {
			clients[id] = client
		}
	}
	return clients, members
}

// OrgRevoke handles DELETE /v1/orgs/{slug}/agent-connections/{id}.
func OrgRevoke(d OrgDeps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		orgID, ok := d.activeOrg(w, r, authz.OrgActionRevokeApiKey)
		if !ok {
			return
		}
		k, err := d.Store.GetOrgAPIKey(r.Context(), orgID, r.PathValue("id"))
		if errors.Is(err, state.ErrNotFound) || (err == nil && Kind(k) == "") {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Agent connection not found", "no connected agent with this id acts in this organization"))
			return
		}
		if err == nil {
			err = d.RevokeOrg(r, acct, orgID, k.ID, "agent_disconnected")
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not revoke agent connection"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// OrgRevokeAll handles POST /v1/orgs/{slug}/agent-connections/revoke_all.
func OrgRevokeAll(d OrgDeps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		orgID, ok := d.activeOrg(w, r, authz.OrgActionRevokeApiKey)
		if !ok {
			return
		}
		revoked, err := d.revokeWhere(r, acct, orgID, "agent_disconnected", func(state.APIKey) bool { return true })
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not revoke every agent connection"))
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, api.AgentConnectionsRevokeAllResponse{Revoked: revoked})
	}
}

// revokeWhere revokes the organization's agent connections that match.
func (d OrgDeps) revokeWhere(r *http.Request, acct state.Account, orgID, reason string, match func(state.APIKey) bool) (int, error) {
	keys, err := orgConnected(r.Context(), d.Store, orgID)
	revoked := 0
	for i := 0; err == nil && i < len(keys); i++ {
		if !match(keys[i]) {
			continue
		}
		if err = d.RevokeOrg(r, acct, orgID, keys[i].ID, reason); err == nil {
			revoked++
		}
	}
	return revoked, err
}

// GetPolicy handles GET /v1/orgs/{slug}/agent-policy.
func GetPolicy(d OrgDeps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		orgID, ok := d.activeOrg(w, r, authz.OrgActionView)
		if !ok {
			return
		}
		store, ok := d.Store.(state.OrgAgentStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("agent policies unavailable"))
			return
		}
		policy, err := store.OrgAgentPolicy(r.Context(), orgID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read the agent policy"))
			return
		}
		channels, err := boundChannelNames(r.Context(), d.Store, acct.ID, orgID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read the approval channels"))
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, policyResponse(policy, 0, channels))
	}
}

// PutPolicy handles PUT /v1/orgs/{slug}/agent-policy. Agent connections the
// new policy no longer allows are revoked in the same request.
func PutPolicy(d OrgDeps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		orgID, ok := d.activeOrg(w, r, authz.OrgActionRevokeApiKey)
		if !ok {
			return
		}
		var req api.OrgAgentPolicyRequest
		if err := httpkit.DecodeJSON(r, &req); err != nil || req.AgentsAllowed == nil || req.AllowedScopes == nil || !ValidPolicyScopes(*req.AllowedScopes) {
			api.WriteProblem(w, api.ErrValidation("agents_allowed and allowed_scopes are required; allowed_scopes lists grantable scopes once each: "+joinScopes()))
			return
		}
		store, ok := d.Store.(state.OrgAgentStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("agent policies unavailable"))
			return
		}
		approvals, prob := d.policyApprovals(r.Context(), store, orgID, req.ApprovalCategories)
		var maxBudget *int64
		if prob == nil {
			maxBudget, prob = policyMaxBudget(r.Context(), store, orgID, req)
		}
		if prob == nil && req.ApprovalChannels != nil {
			// The caller's own channels get the organization's requests (ADR-20261107140000 §3).
			prob = setApprovalChannels(r.Context(), d.Store, acct.ID, orgID, *req.ApprovalChannels)
		}
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		policy, err := store.SetOrgAgentPolicy(r.Context(), state.OrgAgentPolicy{OrgID: orgID, AgentsAllowed: *req.AgentsAllowed, AllowedScopes: *req.AllowedScopes, ApprovalCategories: approvals, MaxAgentBudgetCents: maxBudget, UpdatedBy: acct.ID})
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not save the agent policy"))
			return
		}
		revoked, err := d.revokeWhere(r, acct, orgID, "agent_policy", func(k state.APIKey) bool { return ExceedsPolicy(policy, k.Scopes) })
		d.RecordPolicy(r, acct, policy, revoked)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("the policy is saved, but some agent connections it no longer allows could not be revoked; retry"))
			return
		}
		channels, _ := boundChannelNames(r.Context(), d.Store, acct.ID, orgID)
		httpkit.WriteJSON(w, http.StatusOK, policyResponse(policy, revoked, channels))
	}
}

func policyResponse(policy state.OrgAgentPolicy, revoked int, channels []string) api.OrgAgentPolicyResponse {
	out := api.OrgAgentPolicyResponse{AgentsAllowed: policy.AgentsAllowed, AllowedScopes: slices.Clone(policy.AllowedScopes), Revoked: revoked}
	out.ApprovalChannels = append([]string{}, channels...)
	if out.AllowedScopes == nil {
		out.AllowedScopes = []string{}
	}
	out.ApprovalCategories = append([]string{}, policy.ApprovalCategories...)
	out.MaxAgentBudgetCents = policy.MaxAgentBudgetCents
	if !policy.UpdatedAt.IsZero() {
		out.UpdatedAt = policy.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func joinScopes() string {
	out := ""
	for i, scope := range api.MCPGrantableScopes {
		if i > 0 {
			out += ", "
		}
		out += scope
	}
	return out
}

// policyMaxBudget is the agent budget ceiling a policy change sets: the
// requested one, none when cleared, or the current one when the request
// names neither (ADR-20261111114059).
func policyMaxBudget(ctx context.Context, store state.OrgAgentStore, orgID string, req api.OrgAgentPolicyRequest) (*int64, *api.Problem) {
	switch {
	case req.ClearMaxAgentBudget && req.MaxAgentBudgetCents != nil:
		return nil, api.ErrValidation("set max_agent_budget_cents or clear_max_agent_budget, not both")
	case req.ClearMaxAgentBudget:
		return nil, nil
	case req.MaxAgentBudgetCents != nil:
		if c := *req.MaxAgentBudgetCents; c < 0 || c > api.MaxAgentBudgetCents {
			return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid agent budget ceiling",
				"max_agent_budget_cents must be between 0 and the maximum").WithLimit(api.MaxAgentBudgetCents, c)
		}
		return req.MaxAgentBudgetCents, nil
	}
	current, err := store.OrgAgentPolicy(ctx, orgID)
	if err != nil {
		return nil, api.ErrCapacity("could not load the agent policy")
	}
	return current.MaxAgentBudgetCents, nil
}

// policyApprovals is the floor a policy change sets: the requested
// categories, or the current ones when the request names none
// (ADR-20261104140058).
func (d OrgDeps) policyApprovals(ctx context.Context, store state.OrgAgentStore, orgID string, requested *[]string) ([]string, *api.Problem) {
	if requested == nil {
		current, err := store.OrgAgentPolicy(ctx, orgID)
		if err != nil {
			return nil, api.ErrCapacity("could not load the agent policy")
		}
		return current.ApprovalCategories, nil
	}
	categories, err := api.NormalizeAgentApprovalCategories(*requested)
	if err != nil {
		return nil, api.ErrValidation(err.Error())
	}
	return categories, nil
}
