package toolregistry

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant"
)

type GrantAudit func(context.Context, string, string, map[string]any)

// Grants is owner-only; an agent cannot delegate its authority further.
func Grants(store state.Store, audit GrantAudit) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		if !accountAccess(w, r, acct, store) {
			return
		}
		if p, _ := httpkit.PrincipalFrom(r); p.Key != nil && !httpkit.APIKeyCarriesScope(*p.Key, api.ScopeAdmin) {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Owner authority required", "tool delegation requires an unscoped personal administrator"))
			return
		}
		journal, ok := store.(state.ToolGrantStore)
		if !ok {
			grantRespond(w, nil, errors.New("tool grant store unavailable"))
			return
		}
		id := r.PathValue("id")
		if id == "" && r.Method == http.MethodGet {
			listGrants(w, r, acct, journal)
			return
		}
		if !toolgrant.ValidID(id) {
			grantRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		switch r.Method {
		case http.MethodPut:
			putGrant(w, r, acct, store, journal, id, audit)
		case http.MethodGet:
			out, err := journal.GetToolGrant(r.Context(), acct.ID, id)
			grantRespond(w, out, err)
		case http.MethodDelete:
			out, err := journal.RevokeToolGrant(r.Context(), acct.ID, id)
			if err == nil {
				auditGrant(r, acct, "tool_grant.revoked", out, audit)
			}
			grantRespond(w, out, err)
		}
	}
}

func listGrants(w http.ResponseWriter, r *http.Request, acct state.Account, journal state.ToolGrantStore) {
	limit := api.ToolGrantPageMax
	var err error
	if r.URL.Query().Has("limit") {
		limit, err = strconv.Atoi(r.URL.Query().Get("limit"))
	}
	if err != nil {
		grantRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	out, err := journal.ListToolGrants(r.Context(), acct.ID, r.URL.Query().Get("cursor"), limit)
	grantRespond(w, out, err)
}

func putGrant(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, journal state.ToolGrantStore, id string, audit GrantAudit) {
	var in api.ToolGrantIntent
	if httpkit.DecodeJSONSized(r, &in, api.ToolGrantIntentMaxBytes) != nil || toolgrant.ValidateIntent(in) != nil {
		grantRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	in.ExpiresAt = in.ExpiresAt.UTC().Truncate(time.Microsecond)
	if old, err := journal.GetToolGrant(r.Context(), acct.ID, id); err == nil {
		if !toolgrant.SameIntent(old.Intent, in) {
			grantRespond(w, nil, state.ErrConflict)
			return
		}
		grantRespond(w, old, nil)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		grantRespond(w, nil, err)
		return
	}
	out, err := reviewGrant(r, acct, store, id, in)
	if err == nil {
		out, err = journal.PutToolGrant(r.Context(), out)
	}
	if err == nil {
		auditGrant(r, acct, "tool_grant.put", out, audit)
	}
	grantRespond(w, out, err)
}

func reviewGrant(r *http.Request, acct state.Account, store state.Store, id string, in api.ToolGrantIntent) (api.ToolGrantRecord, error) {
	now := time.Now().UTC()
	out := api.ToolGrantRecord{ID: id, AccountID: acct.ID, Intent: in, CreatedAt: now}
	if !in.ExpiresAt.After(now) || in.ExpiresAt.Sub(now) > api.ToolGrantMaxLifetime {
		return out, state.ErrInvalidArgument
	}
	key, err := grantKey(r, acct, store, in.KeyID)
	if err != nil {
		return out, err
	}
	if key.ExpiresAt != nil && in.ExpiresAt.After(*key.ExpiresAt) {
		return out, state.ErrInvalidArgument
	}
	publications, ok := store.(state.ToolPublicationStore)
	if !ok {
		return out, errors.New("publication store unavailable")
	}
	p, err := publications.GetToolPublication(r.Context(), acct.ID, in.Version)
	if err != nil {
		return out, err
	}
	if p.Status != "completed" || p.Outcome == nil || !validCatalog(p.Outcome.Catalog) {
		return out, state.ErrNotFound
	}
	if _, ok := p.Intent.ToolScopes[in.Name]; !ok {
		return out, state.ErrNotFound
	}
	app, err := publicationApp(r, acct, store, p)
	if err != nil {
		return out, err
	}
	if !keyAllowsApp(key, app) {
		return out, state.ErrNotFound
	}
	out.App, out.AppID, out.ProjectID = app.Slug, app.ID, app.ProjectID
	err = bindGrantCredential(r.Context(), store, &out, p)
	return out, err
}

func auditGrant(r *http.Request, acct state.Account, kind string, out api.ToolGrantRecord, audit GrantAudit) {
	if audit != nil {
		audit(r.Context(), kind, acct.ID, map[string]any{"grant_id": out.ID, "key_id": out.Intent.KeyID, "version": out.Intent.Version, "tool": out.Intent.Name, "app_id": out.AppID, "allow_invocation": out.Intent.AllowInvocation, "expires_at": out.Intent.ExpiresAt})
	}
}

func grantRespond(w http.ResponseWriter, out any, err error) {
	var limit *state.ToolGrantLimitError
	switch {
	case errors.As(err, &limit):
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "tool_grant_limit", "Tool grant capacity reached", "retained grant identities are bounded per connection and account").WithLimit(limit.Limit, limit.Observed).WithDocs("https://gregale.dev/docs/mcp#scoped-tool-grants"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Tool access not found", "no matching accessible tool, connection or grant"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Tool grant changed", "a grant identity is immutable; use a new reviewed identity"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("an exact version/tool, agent key, canonical grant UUID and expiry within 30 days are required"))
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("tool access is unavailable"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
