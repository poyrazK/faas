package mcpreadiness

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// PolicyHistory uses the same owned-app loader as policy reads/writes. The router
// applies admin scope and MFA to restore; reads use the read surface scopes.
func (h *Handler) PolicyHistory(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := h.Store.(state.MCPReadinessPolicyHistoryStore)
	if !ok {
		problem(w, errors.New("store unavailable"))
		return
	}
	app, e := h.Store.AppBySlug(r.Context(), r.PathValue("slug"))
	if e != nil || app.AccountID != account.ID {
		problem(w, state.ErrNotFound)
		return
	}
	if h.Loader != nil {
		if _, allowed := h.Loader.LoadApp(w, r, account, app.Slug); !allowed {
			return
		}
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	allowed := map[string]bool{"scope": true}
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/diff") {
		allowed["from_revision"] = true
		allowed["to_revision"] = true
	} else if r.Method == http.MethodGet && r.PathValue("revision") == "" {
		allowed["before_revision"] = true
		allowed["limit"] = true
	}
	for key, values := range query {
		if len(values) != 1 || !allowed[key] {
			problem(w, state.ErrInvalidArgument)
			return
		}
	}
	scope := api.MCPConnectionScope(query.Get("scope"))
	if api.ValidateScope(scope) != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	revision := r.PathValue("revision")
	if revision != "" && uuid.Validate(revision) != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	if r.Method == http.MethodPost {
		var request api.RestoreMCPReadinessPolicyRequest
		if !decode(w, r, &request) {
			return
		}
		if query.Has("scope") {
			if request.Scope != "" && request.Scope != scope {
				problem(w, state.ErrInvalidArgument)
				return
			}
			request.Scope = scope
		}
		if api.ValidateRestoreMCPReadinessPolicy(request, revision) != nil {
			problem(w, state.ErrInvalidArgument)
			return
		}
		old, err := store.GetMCPReadinessPolicyRevision(r.Context(), account.ID, app.ID, api.MCPConnectionScope(request.Scope), revision)
		if err != nil {
			problem(w, err)
			return
		}
		scheduled := false
		for _, role := range old.Roles {
			if role.Monitor != nil && role.Monitor.Enabled {
				scheduled = true
			}
		}
		if scheduled && app.PublicAuthMode == state.AppPublicAuthModeInternalOnly || (old.Enabled || scheduled) && !app.AcceptsRequestInvocations() {
			problem(w, state.ErrInvalidArgument)
			return
		}
		restored, err := store.RestoreMCPReadinessPolicy(r.Context(), account.ID, app.ID, revision, request)
		if err != nil {
			problem(w, err)
			return
		}
		httpkit.WriteJSON(w, 200, restored)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/diff") {
		from, to := query.Get("from_revision"), query.Get("to_revision")
		if uuid.Validate(from) != nil || to != "" && uuid.Validate(to) != nil {
			problem(w, state.ErrInvalidArgument)
			return
		}
		left, err := store.GetMCPReadinessPolicyRevision(r.Context(), account.ID, app.ID, scope, from)
		if err != nil {
			problem(w, err)
			return
		}
		var right api.MCPReadinessPolicy
		if to != "" {
			right, err = store.GetMCPReadinessPolicyRevision(r.Context(), account.ID, app.ID, scope, to)
		} else {
			right, err = store.GetMCPReadinessPolicy(r.Context(), account.ID, app.ID, scope)
		}
		if err != nil {
			problem(w, err)
			return
		}
		httpkit.WriteJSON(w, 200, api.DiffMCPReadinessPolicies(left, right))
		return
	}
	if revision != "" {
		p, err := store.GetMCPReadinessPolicyRevision(r.Context(), account.ID, app.ID, scope, revision)
		if err != nil {
			problem(w, err)
			return
		}
		httpkit.WriteJSON(w, 200, p)
		return
	}
	limit := 20
	if query.Has("limit") {
		limit, e = strconv.Atoi(query.Get("limit"))
		if e != nil {
			problem(w, state.ErrInvalidArgument)
			return
		}
	}
	page, err := store.ListMCPReadinessPolicyHistory(r.Context(), account.ID, app.ID, scope, query.Get("before_revision"), limit)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, page)
}
