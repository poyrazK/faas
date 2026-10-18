package mcpreadiness

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) Attention(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, err := h.Store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != account.ID {
		problem(w, state.ErrNotFound)
		return
	}
	if h.Loader != nil {
		if _, ok := h.Loader.LoadApp(w, r, account, app.Slug); !ok {
			return
		}
	}
	source, ok := h.Store.(state.MCPReadinessAttentionStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("incident attention store unavailable"))
		return
	}
	id := r.PathValue("id")
	if id != "" && uuid.Validate(id) != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	if r.Method == http.MethodGet {
		attentionRead(w, r, account, app, source, id)
		return
	}
	if r.URL.RawQuery != "" || id == "" {
		problem(w, state.ErrInvalidArgument)
		return
	}
	h.attentionDecision(w, r, account, app, source, id)
}
func attentionRead(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, source state.MCPReadinessAttentionStore, id string) {
	q, limit, include, err := attentionQuery(r, id)
	if err != nil {
		problem(w, err)
		return
	}
	var out any
	if id != "" {
		out, err = source.ListMCPReadinessAttentionHistory(r.Context(), account.ID, app.ID, id, q.Get("before"), limit)
	} else {
		out, err = source.ListMCPReadinessAttention(r.Context(), account.ID, app.ID, q.Get("scope"), q.Get("after"), limit, include, time.Now().UTC())
	}
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func attentionQuery(r *http.Request, id string) (url.Values, int, bool, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, 0, false, state.ErrInvalidArgument
	}
	allowed := map[string]bool{"limit": true}
	if id == "" {
		allowed["scope"] = true
		allowed["after"] = true
		allowed["include_resolved"] = true
	} else {
		allowed["before"] = true
	}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			return nil, 0, false, state.ErrInvalidArgument
		}
	}
	limit := 20
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			return nil, 0, false, state.ErrInvalidArgument
		}
	}
	if q.Has("scope") && api.ValidateScope(q.Get("scope")) != nil {
		return nil, 0, false, state.ErrInvalidArgument
	}
	include := false
	if q.Has("include_resolved") {
		include, err = strconv.ParseBool(q.Get("include_resolved"))
		if err != nil {
			return nil, 0, false, state.ErrInvalidArgument
		}
	}
	return q, limit, include, nil
}
func (h *Handler) attentionDecision(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, source state.MCPReadinessAttentionStore, id string) {
	var request api.MCPReadinessAttentionAction
	if !decode(w, r, &request) {
		return
	}
	receipt, err := source.ActMCPReadinessAttention(r.Context(), account.ID, app.ID, id, request, time.Now().UTC())
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Incident attention changed", "Refresh incident attention and its version. New decisions require an open incident; identical request retries return their original receipt."))
		return
	}
	if err != nil {
		problem(w, err)
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "mcp.readiness_attention."+receipt.Action, &account.ID, map[string]any{"app_id": app.ID, "incident_id": receipt.IncidentID, "request_id": receipt.RequestID, "version": receipt.Version, "reason": receipt.Reason})
	}
	httpkit.WriteJSON(w, http.StatusOK, receipt)
}
