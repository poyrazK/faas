// adr: 20261103222927
package agentinspect

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func Inventory(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		w.Header().Set("Cache-Control", "private, no-store")
		q, p := query(r, "kind", "environment", "limit", "offset")
		if p != nil {
			api.WriteProblem(w, p)
			return
		}
		kind, limit, offset, p := inventoryQuery(q)
		if p != nil {
			api.WriteProblem(w, p)
			return
		}
		app, env, ok := d.Resolve(w, r, account)
		if !ok {
			return
		}
		if d.Released != nil && !d.Released(r, account, app, inventoryPolicy(kind)) {
			api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Not found", "selected resource inventory is unavailable"))
			return
		}
		scope := api.DefaultEnvScope
		if env.ID != "" {
			scope = env.Slug
		}
		request := state.AppResourceInventoryRequest{AccountID: app.AccountID, AppID: app.ID, OrgID: app.OrgID, Scope: scope, Kind: kind, Limit: limit, Offset: offset}
		page, err := readInventory(d, r, request)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read selected application resource metadata"))
			return
		}
		if p := inventoryRecheck(d, r, app, env); p != nil {
			api.WriteProblem(w, p)
			return
		}
		if !validInventoryPage(page, request) {
			api.WriteProblem(w, api.ErrCapacity("selected resource metadata was incompatible"))
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, inventoryResponse(page, request, env))
	}
}

func inventoryQuery(q url.Values) (string, int, int, *api.Problem) {
	kind := q.Get("kind")
	if !api.ValidAppResourceKind(kind) {
		return "", 0, 0, api.ErrValidation("kind must be domains, crons, buckets, storage_bindings or databases")
	}
	if (kind == "domains" || kind == "crons") && q.Get("environment") != "" {
		return "", 0, 0, api.ErrValidation("domain and cron inventory is app-wide; omit environment")
	}
	limit, offset := api.AppResourceInventoryDefaultPage, 0
	var err error
	if v, ok := q["limit"]; ok {
		limit, err = strconv.Atoi(v[0])
		if err != nil {
			return "", 0, 0, api.ErrValidation("limit must be an integer")
		}
	}
	if v, ok := q["offset"]; ok {
		offset, err = strconv.Atoi(v[0])
		if err != nil {
			return "", 0, 0, api.ErrValidation("offset must be an integer")
		}
	}
	if limit < 1 || limit > api.AppResourceInventoryMaxPage || offset < 0 || offset > api.AppResourceInventoryMaxOffset {
		return "", 0, 0, api.ErrValidation("inventory limit or offset is outside its allowed bounds")
	}
	return kind, limit, offset, nil
}

func readInventory(d Deps, r *http.Request, q state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error) {
	if d.Inventory != nil {
		return d.Inventory(r, q)
	}
	reader, ok := d.Store.(state.AppResourceInventoryReader)
	if !ok {
		return state.AppResourceInventoryPage{}, state.ErrAppResourceInventoryUnavailable
	}
	return reader.ListAppResourceInventory(r.Context(), q)
}

func inventoryPolicy(kind string) string {
	switch kind {
	case "domains":
		return "GET /v1/domains"
	case "crons":
		return "GET /v1/crons"
	case "buckets", "storage_bindings":
		return "GET /v1/apps/{slug}/buckets"
	default:
		return "GET /v1/postgres/databases"
	}
}

func inventoryRecheck(d Deps, r *http.Request, app state.App, env state.ProjectEnvironment) *api.Problem {
	if p := recheck(d, r, app, "GET /v1/apps/{slug}/resource-inventory"); p != nil {
		return p
	}
	if env.ID == "" {
		return nil
	}
	fresh, err := d.Store.ProjectEnvironmentBySlug(r.Context(), app.AccountID, env.ProjectID, env.Slug)
	if errors.Is(err, state.ErrNotFound) || err == nil && fresh.ID != env.ID {
		return api.NewProblem(404, api.CodeNotFound, "Not found", "selected environment identity changed")
	}
	if err != nil {
		return api.ErrCapacity("could not recheck selected environment")
	}
	return nil
}

func inventoryResponse(page state.AppResourceInventoryPage, q state.AppResourceInventoryRequest, env state.ProjectEnvironment) api.AppResourceInventoryResponse {
	out := api.AppResourceInventoryResponse{SchemaVersion: 1, AppID: q.AppID, Scope: q.Scope, ProjectID: env.ProjectID, EnvironmentID: env.ID, Kind: q.Kind, Limit: q.Limit, Offset: q.Offset, Total: page.Total, Items: page.Items, HasMore: page.HasMore, GeneratedAt: time.Now().UTC()}
	if page.HasMore {
		next := q.Offset + len(page.Items)
		out.NextOffset = &next
	}
	return out
}

func validInventoryPage(p state.AppResourceInventoryPage, q state.AppResourceInventoryRequest) bool {
	if p.Total < 0 || p.Items == nil || len(p.Items) > q.Limit {
		return false
	}
	end := int64(q.Offset) + int64(len(p.Items))
	if p.HasMore && (len(p.Items) != q.Limit || p.Total <= end) || !p.HasMore && (int64(q.Offset) <= p.Total && end != p.Total || int64(q.Offset) > p.Total && len(p.Items) != 0) {
		return false
	}
	for _, v := range p.Items {
		if v.ID == "" || v.AppID != q.AppID || v.Scope != q.Scope {
			return false
		}
		if b := v.Binding; b != nil && (b.AppID != q.AppID || b.Scope != q.Scope || b.DatabaseID != v.ID || b.ID == "") {
			return false
		}
	}
	return true
}
