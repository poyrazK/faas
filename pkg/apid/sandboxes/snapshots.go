package sandboxes

// Persisted workspaces (ADR-20261027090013 step 7): list, read and delete
// the account's snapshots. agentd writes them when a session persists its
// workspace; a delete here is a request agentd's sweep carries out, and the
// snapshot stops being listed or usable at once.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const (
	snapshotPageDefault = 50
	snapshotPageMax     = 200
)

// ListSnapshots handles GET /v1/sandbox-snapshots, newest first; before
// pages from a snapshot's ID.
func (h *Handler) ListSnapshots(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	f := agentstore.SandboxSnapshotFilter{Limit: snapshotPageDefault}
	q := r.URL.Query()
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > snapshotPageMax {
			api.WriteProblem(w, api.ErrValidation("limit must be 1-200"))
			return
		}
		f.Limit = n
	}
	if before := q.Get("before"); before != "" {
		snap, err := h.Store.SandboxSnapshot(r.Context(), acct.ID, before)
		if err != nil {
			api.WriteProblem(w, h.snapshotProblem(acct, "sandbox.snapshot.list", err))
			return
		}
		f.Before = &snap
	}
	limit := f.Limit
	f.Limit++
	rows, err := h.Store.SandboxSnapshots(r.Context(), acct.ID, f)
	if err != nil {
		api.WriteProblem(w, h.snapshotProblem(acct, "sandbox.snapshot.list", err))
		return
	}
	out := api.SandboxSnapshotList{Items: []api.SandboxSnapshot{}}
	for i, snap := range rows {
		if i == limit {
			out.NextBefore = rows[i-1].ID
			break
		}
		out.Items = append(out.Items, snapshotView(snap))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// GetSnapshot handles GET /v1/sandbox-snapshots/{id}.
func (h *Handler) GetSnapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	snap, err := h.Store.SandboxSnapshot(r.Context(), acct.ID, r.PathValue("id"))
	if err == nil && snap.DeleteRequestedAt != nil {
		err = agentstore.ErrNotFound
	}
	if err != nil {
		api.WriteProblem(w, h.snapshotProblem(acct, "sandbox.snapshot.get", err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, snapshotView(snap))
}

// DeleteSnapshot handles DELETE /v1/sandbox-snapshots/{id}. Deleting a
// snapshot already being deleted changes nothing.
func (h *Handler) DeleteSnapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	id := r.PathValue("id")
	if err := h.Store.RequestSandboxSnapshotDelete(r.Context(), acct.ID, id); err != nil {
		api.WriteProblem(w, h.snapshotProblem(acct, "sandbox.snapshot.delete", err))
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "sandbox.snapshot.deleted", acct.ID, map[string]any{"snapshot_id": id})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) snapshotProblem(acct state.Account, operation string, err error) *api.Problem {
	if errors.Is(err, agentstore.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeSandboxSnapshotNotFound, "Snapshot not found", "the account has no such snapshot")
	}
	return h.problem(acct, operation, err)
}

func snapshotView(s agentstore.SandboxSnapshot) api.SandboxSnapshot {
	return api.SandboxSnapshot{ID: s.ID, SandboxID: s.SandboxID, State: s.State, Bytes: s.Bytes, SHA256: s.SHA256,
		ExpiresAt: s.ExpiresAt, CreatedAt: s.CreatedAt, ReadyAt: s.ReadyAt}
}
