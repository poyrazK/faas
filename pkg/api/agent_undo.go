package api

import (
	"context"
	"net/http"
	"net/url"
)

// Agent undo (ADR-20261112080601): reverse what an agent key changed.

// Kinds of agent change an undo record describes.
const (
	AgentUndoKindSettings = "settings"
	AgentUndoKindDeploy   = "deploy"
	AgentUndoKindCreate   = "create"
	AgentUndoKindDelete   = "delete"
	AgentUndoKindOther    = "other"
)

// Statuses of a change in an undo plan.
const (
	// AgentUndoUndoable: undoing it will reverse the change.
	AgentUndoUndoable = "undoable"
	// AgentUndoNotUndoable: Gregale cannot reverse this change; Reason says why.
	AgentUndoNotUndoable = "not_undoable"
	// AgentUndoChangedSince: someone changed the resource after the agent
	// did; undo leaves it unless forced.
	AgentUndoChangedSince = "changed_since"
	// AgentUndoUndone: the change was already undone.
	AgentUndoUndone = "undone"
)

// Results of applying an undo to one change.
const (
	AgentUndoResultUndone       = "undone"
	AgentUndoResultChangedSince = "changed_since"
	AgentUndoResultFailed       = "failed"
	// AgentUndoResultSkipped: a newer change to the same resource could not
	// be undone, so this older one was left as well.
	AgentUndoResultSkipped = "skipped"
)

// AgentUndoRequest chooses the changes to undo: the key's changes since a
// time (default the last 24 hours), or the activity entries named.
type AgentUndoRequest struct {
	// Since is an RFC 3339 time; empty means DefaultAgentUndoWindow ago.
	Since string `json:"since,omitempty"`
	// Entries are activity entry ids; when given, only they are undone.
	Entries []string `json:"entries,omitempty"`
	// Force undoes changes someone else changed again since.
	Force bool `json:"force,omitempty"`
	// PlanDigest, on apply, is the digest of the plan the person reviewed.
	PlanDigest string `json:"plan_digest,omitempty"`
}

// AgentUndoItem is one agent change in an undo plan or result.
type AgentUndoItem struct {
	ActivityID string `json:"activity_id"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Summary    string `json:"summary"`
	CreatedAt  string `json:"created_at"`
	Status     string `json:"status"`
	// Reason explains not_undoable and changed_since.
	Reason string `json:"reason,omitempty"`
	// Result is set on apply.
	Result string `json:"result,omitempty"`
	// ErrorCode and Error say why an apply failed.
	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
}

// AgentUndoPlan lists an agent's changes, newest first, and what undoing
// them will do. Apply with the same request plus PlanDigest.
type AgentUndoPlan struct {
	Since      string          `json:"since"`
	Items      []AgentUndoItem `json:"items"`
	Undoable   int             `json:"undoable"`
	PlanDigest string          `json:"plan_digest"`
}

// AgentUndoResult is what an applied undo did, newest first.
type AgentUndoResult struct {
	Items  []AgentUndoItem `json:"items"`
	Undone int             `json:"undone"`
	Failed int             `json:"failed"`
}

func agentUndoPath(id, verb string) string {
	return "/v1" + orgAgentUndoPath(id, verb)
}

func orgAgentUndoPath(id, verb string) string {
	return "/agent-connections/" + url.PathEscape(id) + "/undo" + verb
}

// PlanAgentUndo plans undoing one of the caller's agents' changes.
func (c *Client) PlanAgentUndo(ctx context.Context, id string, req AgentUndoRequest) (AgentUndoPlan, error) {
	var out AgentUndoPlan
	err := c.do(ctx, http.MethodPost, agentUndoPath(id, "/plan"), req, &out)
	return out, err
}

// ApplyAgentUndo undoes the changes of a reviewed plan (req.PlanDigest).
func (c *Client) ApplyAgentUndo(ctx context.Context, id string, req AgentUndoRequest) (AgentUndoResult, error) {
	var out AgentUndoResult
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, agentUndoPath(id, ""), req, &out)
	return out, err
}

// PlanOrgAgentUndo and ApplyOrgAgentUndo are the organization's variants,
// for an owner or admin.
func (c *Client) PlanOrgAgentUndo(ctx context.Context, org, id string, req AgentUndoRequest) (AgentUndoPlan, error) {
	var out AgentUndoPlan
	err := c.do(ctx, http.MethodPost, "/v1/orgs/"+url.PathEscape(org)+orgAgentUndoPath(id, "/plan"), req, &out)
	return out, err
}

func (c *Client) ApplyOrgAgentUndo(ctx context.Context, org, id string, req AgentUndoRequest) (AgentUndoResult, error) {
	var out AgentUndoResult
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/orgs/"+url.PathEscape(org)+orgAgentUndoPath(id, ""), req, &out)
	return out, err
}
