package agentconnections

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261112080601: a person undoes what one of their agents changed.

// Undoer plans and applies undos; apidserver's agentundo.Engine.
type Undoer interface {
	Plan(ctx context.Context, base *http.Request, acct state.Account, keyID string, req api.AgentUndoRequest) (api.AgentUndoPlan, *api.Problem)
	Apply(ctx context.Context, base *http.Request, acct state.Account, keyID string, req api.AgentUndoRequest) (api.AgentUndoResult, *api.Problem)
}

// UndoPlan is POST /v1/agent-connections/{id}/undo/plan: what undoing one of
// the caller's agents' changes would do.
func UndoPlan(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		k, req, ok := d.undoTarget(w, r, acct)
		if !ok {
			return
		}
		writeUndoPlan(w, r, d, acct, k, req)
	}
}

// Undo is POST /v1/agent-connections/{id}/undo: apply a reviewed plan.
func Undo(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		k, req, ok := d.undoTarget(w, r, acct)
		if !ok {
			return
		}
		applyUndo(w, r, d, acct, k, req)
	}
}

// OrgUndoPlan and OrgUndo are the organization's variants: an owner or admin
// undoes an agent that acts in the organization.
func OrgUndoPlan(d OrgDeps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		k, req, ok := d.orgUndoTarget(w, r)
		if !ok {
			return
		}
		writeUndoPlan(w, r, d.Deps, acct, k, req)
	}
}

func OrgUndo(d OrgDeps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		k, req, ok := d.orgUndoTarget(w, r)
		if !ok {
			return
		}
		applyUndo(w, r, d.Deps, acct, k, req)
	}
}

func writeUndoPlan(w http.ResponseWriter, r *http.Request, d Deps, acct state.Account, k state.APIKey, req api.AgentUndoRequest) {
	plan, problem := d.Undo.Plan(r.Context(), r, acct, k.ID, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, plan)
}

func applyUndo(w http.ResponseWriter, r *http.Request, d Deps, acct state.Account, k state.APIKey, req api.AgentUndoRequest) {
	result, problem := d.Undo.Apply(r.Context(), r, acct, k.ID, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if d.Audit != nil {
		d.Audit(r.Context(), "agent.undo_applied", acct.ID, map[string]any{
			"key_id": k.ID, "undone": result.Undone, "failed": result.Failed, "entries": len(req.Entries), "force": req.Force,
		})
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}

// undoTarget resolves one of the caller's agent connections. An agent key
// cannot undo (agents request undos through approval, a later slice).
func (d Deps) undoTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.APIKey, api.AgentUndoRequest, bool) {
	if _, isAgent := agentKey(r); isAgent {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Agent key cannot undo", "a person undoes an agent's changes"))
		return state.APIKey{}, api.AgentUndoRequest{}, false
	}
	req, ok := d.undoRequest(w, r)
	if !ok {
		return state.APIKey{}, req, false
	}
	k, err := ownConnection(r.Context(), d.Store, acct.ID, r.PathValue("id"))
	if errors.Is(err, state.ErrNotFound) {
		connectionNotFound(w)
		return state.APIKey{}, req, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the agent connection"))
		return state.APIKey{}, req, false
	}
	return k, req, true
}

func (d OrgDeps) orgUndoTarget(w http.ResponseWriter, r *http.Request) (state.APIKey, api.AgentUndoRequest, bool) {
	if _, isAgent := agentKey(r); isAgent {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Agent key cannot undo", "a person undoes an agent's changes"))
		return state.APIKey{}, api.AgentUndoRequest{}, false
	}
	orgID, ok := d.activeOrg(w, r, authz.OrgActionRevokeApiKey)
	if !ok {
		return state.APIKey{}, api.AgentUndoRequest{}, false
	}
	req, ok := d.undoRequest(w, r)
	if !ok {
		return state.APIKey{}, req, false
	}
	k, err := d.Store.GetOrgAPIKey(r.Context(), orgID, r.PathValue("id"))
	if errors.Is(err, state.ErrNotFound) || (err == nil && Kind(k) == "") {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Agent connection not found", "no connected agent with this id acts in this organization"))
		return state.APIKey{}, req, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the agent connection"))
		return state.APIKey{}, req, false
	}
	return k, req, true
}

func (d Deps) undoRequest(w http.ResponseWriter, r *http.Request) (api.AgentUndoRequest, bool) {
	var req api.AgentUndoRequest
	if d.Undo == nil {
		api.WriteProblem(w, api.ErrCapacity("agent undo is unavailable"))
		return req, false
	}
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid undo request"))
		return req, false
	}
	if len(req.Entries) > api.MaxAgentUndoRecords {
		api.WriteProblem(w, api.ErrValidation("name at most 500 entries"))
		return req, false
	}
	return req, true
}
