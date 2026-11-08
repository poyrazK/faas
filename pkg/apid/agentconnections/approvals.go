package agentconnections

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/agentapproval"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Human approval of agents' risky requests (ADR-20261104140058).

const approvalListMax = 50

// MayDecide reports whether a person may approve or deny an agent's request:
// the member who connected the agent, or an owner or admin of the
// organization it acts in.
func MayDecide(ctx context.Context, store state.Store, approval state.AgentApproval, accountID string) (bool, error) {
	if accountID == "" {
		return false, nil
	}
	if approval.AccountID == accountID {
		return true, nil
	}
	org, err := store.OrgByID(ctx, approval.OrgID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if org.Personal {
		return false, nil
	}
	member, err := store.OrgAccessSnapshot(ctx, approval.OrgID, accountID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return member.Role == state.OrgRoleOwner || member.Role == state.OrgRoleAdmin, nil
}

// decidableOrgs are the shared organizations whose agents' requests the
// account may decide.
func decidableOrgs(ctx context.Context, store state.Store, accountID string) ([]string, error) {
	orgs, err := store.ListOrgsForAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, org := range orgs {
		if org.Personal {
			continue
		}
		member, err := store.OrgAccessSnapshot(ctx, org.ID, accountID)
		if err != nil {
			continue
		}
		if member.Role == state.OrgRoleOwner || member.Role == state.OrgRoleAdmin {
			out = append(out, org.ID)
		}
	}
	return out, nil
}

// agentKey reports whether the request's key is an agent connection's.
func agentKey(r *http.Request) (*state.APIKey, bool) {
	p, ok := httpkit.PrincipalFrom(r)
	if !ok || p.Key == nil {
		return nil, false
	}
	if slices.Contains(p.Key.Scopes, api.ScopeAgent) || p.Key.Label == api.MCPLoginKeyLabel {
		return p.Key, true
	}
	return nil, false
}

// GetApproval is GET /v1/agent-approvals/{id}: an agent reads its own
// request; a person reads one they may decide.
func GetApproval(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		store, ok := d.Store.(state.AgentApprovalStore)
		notFound := func() {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such approval request"))
		}
		if !ok {
			notFound()
			return
		}
		approval, err := store.AgentApproval(r.Context(), r.PathValue("id"))
		if errors.Is(err, state.ErrNotFound) {
			notFound()
			return
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not load the approval request"))
			return
		}
		if key, isAgent := agentKey(r); isAgent {
			if approval.KeyID != key.ID {
				notFound()
				return
			}
		} else if may, err := MayDecide(r.Context(), d.Store, approval, acct.ID); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not load the approval request"))
			return
		} else if !may {
			notFound()
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, d.approvalResponse(r.Context(), approval))
	}
}

// ListApprovals is GET /v1/agent-approvals: an agent's own requests, newest
// first, or the pending requests a person may decide.
func ListApprovals(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		out := api.AgentApprovalsResponse{Items: []api.AgentApprovalResponse{}}
		store, ok := d.Store.(state.AgentApprovalStore)
		if !ok {
			httpkit.WriteJSON(w, http.StatusOK, out)
			return
		}
		var approvals []state.AgentApproval
		var err error
		if key, isAgent := agentKey(r); isAgent {
			approvals, err = store.ListAgentApprovalsForKey(r.Context(), key.ID, approvalListMax)
		} else {
			var orgs []string
			if orgs, err = decidableOrgs(r.Context(), d.Store, acct.ID); err == nil {
				approvals, err = store.ListPendingAgentApprovals(r.Context(), acct.ID, orgs, approvalListMax)
			}
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list approval requests"))
			return
		}
		for _, approval := range approvals {
			out.Items = append(out.Items, d.approvalResponse(r.Context(), approval))
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

// approvals lists the kinds of action a connection must have a person
// approve: its own choice (every kind when it chose none) plus its
// organization's floor, in display order. Unreadable settings show every
// kind, as the gate then refuses rather than allows.
func (d Deps) approvals(ctx context.Context, key state.APIKey) []string {
	effective := agentapproval.Effective(nil, false, nil)
	if store, ok := d.Store.(state.AgentApprovalStore); ok {
		own, chosen, err := store.APIKeyApprovalCategories(ctx, key.ID)
		if err == nil {
			var floor []string
			if policies, ok := d.Store.(state.OrgAgentStore); ok && key.OrgID != "" {
				if policy, err := policies.OrgAgentPolicy(ctx, key.OrgID); err == nil {
					floor = policy.ApprovalCategories
				}
			}
			effective = agentapproval.Effective(own, chosen, floor)
		}
	}
	out := []string{}
	for _, category := range api.AgentApprovalCategories {
		if effective[category] {
			out = append(out, category)
		}
	}
	return out
}

// AgentName names the connection behind a request: the client name a hosted
// sign-in gave, else the key's label.
func AgentName(ctx context.Context, store state.Store, approval state.AgentApproval) string {
	if clients, ok := store.(state.AgentClientStore); ok {
		if named, err := clients.APIKeyAgentClients(ctx, approval.AccountID); err == nil {
			if client, ok := named[approval.KeyID]; ok && client.Name != "" {
				return client.Name
			}
		}
	}
	if key, err := store.GetAPIKey(ctx, approval.AccountID, approval.KeyID); err == nil && key.Label != "" {
		return key.Label
	}
	return "an agent"
}

func (d Deps) approvalResponse(ctx context.Context, approval state.AgentApproval) api.AgentApprovalResponse {
	out := api.AgentApprovalResponse{
		ID: approval.ID, Category: approval.Category, Method: approval.Method, Path: approval.Path,
		BodyBound: approval.BodySHA256 != nil, Status: approval.EffectiveStatus(time.Now()),
		Agent:     AgentName(ctx, d.Store, approval),
		CreatedAt: approval.CreatedAt.UTC().Format(time.RFC3339), ExpiresAt: approval.ExpiresAt.UTC().Format(time.RFC3339),
		Note: approval.Note,
	}
	if !approval.DecidedAt.IsZero() {
		out.DecidedAt = approval.DecidedAt.UTC().Format(time.RFC3339)
	}
	if out.Status == api.AgentApprovalPending && d.ReviewURL != nil {
		out.ReviewURL = d.ReviewURL(approval.ID)
	}
	return out
}
