package toolregistry

import (
	"bytes"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func scopedSearch(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store) {
	key, ok := registryKey(w, r, acct, store)
	if !ok {
		return
	}
	limit := api.ToolRegistryPageMax
	q := r.URL.Query()
	var err error
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
	}
	if err != nil || limit < 1 || limit > api.ToolRegistryPageMax || len(q.Get("query")) > api.ToolRegistryQueryMaxBytes || (q.Get("cursor") != "" && !toolpublication.ValidID(q.Get("cursor"))) {
		grantRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	grants, ok := store.(state.ToolGrantStore)
	if !ok {
		grantRespond(w, nil, errors.New("grant store unavailable"))
		return
	}
	entries, err := grants.ToolGrantEntries(r.Context(), acct.ID, key.ID)
	if err != nil {
		grantRespond(w, nil, err)
		return
	}
	versions := map[string]api.ToolRegistryVersion{}
	for _, entry := range entries {
		app, allowed, accessErr := accessibleGrant(r, acct, store, key, entry)
		if accessErr != nil {
			grantRespond(w, nil, accessErr)
			return
		}
		if !allowed {
			continue
		}
		version := entry.Publication.ID
		v, exists := versions[version]
		if !exists {
			v = toolpublication.Version(entry.Publication)
			v.AppID = app.ID
			v.Tools = []string{}
		}
		if !slices.Contains(v.Tools, entry.Grant.Intent.Name) {
			v.Tools = append(v.Tools, entry.Grant.Intent.Name)
		}
		versions[version] = v
	}
	out := api.ToolRegistryPage{Versions: []api.ToolRegistryVersion{}}
	for _, v := range versions {
		if v.Version <= q.Get("cursor") {
			continue
		}
		sort.Strings(v.Tools)
		needle := strings.ToLower(q.Get("query"))
		matches := strings.Contains(strings.ToLower(v.App), needle)
		for _, name := range v.Tools {
			matches = matches || strings.Contains(strings.ToLower(name), needle)
		}
		if matches {
			out.Versions = append(out.Versions, v)
		}
	}
	sort.Slice(out.Versions, func(i, j int) bool { return out.Versions[i].Version < out.Versions[j].Version })
	if len(out.Versions) > limit {
		out.Versions = out.Versions[:limit]
		out.NextCursor = out.Versions[limit-1].Version
	}
	grantRespond(w, out, nil)
}

// Describe returns a single-tool projection. Full publication records and
// unrelated test fixtures remain owner-only.
func Describe(store state.Store) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		version, name := r.PathValue("version"), r.PathValue("name")
		if !toolpublication.ValidID(version) || !toolgrant.ValidName(name) {
			grantRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		var publication api.ToolPublicationRecord
		var app state.App
		var err error
		var access []api.ToolGrantAccess
		if agentRegistryRequest(r) {
			publication, app, access, err = grantedPublication(w, r, acct, store, version, name)
			if err != nil {
				return
			}
		} else {
			if !accountAccess(w, r, acct, store) {
				return
			}
			journal, ok := store.(state.ToolPublicationStore)
			if !ok {
				grantRespond(w, nil, errors.New("publication store unavailable"))
				return
			}
			publication, err = journal.GetToolPublication(r.Context(), acct.ID, version)
			if err == nil {
				app, err = publicationApp(r, acct, store, publication)
			}
			if err != nil {
				grantRespond(w, nil, err)
				return
			}
		}
		out, err := projectTool(publication, app, name)
		out.Grants = access
		grantRespond(w, out, err)
	}
}

func grantedPublication(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, version, name string) (api.ToolPublicationRecord, state.App, []api.ToolGrantAccess, error) {
	key, ok := registryKey(w, r, acct, store)
	if !ok {
		return api.ToolPublicationRecord{}, state.App{}, nil, state.ErrNotFound
	}
	grants, ok := store.(state.ToolGrantStore)
	if !ok {
		err := errors.New("grant store unavailable")
		grantRespond(w, nil, err)
		return api.ToolPublicationRecord{}, state.App{}, nil, err
	}
	var publication api.ToolPublicationRecord
	var application state.App
	access := []api.ToolGrantAccess{}
	entries, err := grants.ToolGrantEntries(r.Context(), acct.ID, key.ID)
	if err == nil {
		for _, entry := range entries {
			if entry.Grant.Intent.Version != version || entry.Grant.Intent.Name != name {
				continue
			}
			app, allowed, accessErr := accessibleGrant(r, acct, store, key, entry)
			if accessErr != nil {
				err = accessErr
				break
			}
			if allowed {
				publication, application = entry.Publication, app
				access = append(access, grantAccess(entry.Grant))
			}
		}
	}
	if err == nil && len(access) > 0 {
		sort.Slice(access, func(i, j int) bool { return access[i].GrantID < access[j].GrantID })
		return publication, application, access, nil
	}
	if err == nil {
		err = state.ErrNotFound
	}
	grantRespond(w, nil, err)
	return api.ToolPublicationRecord{}, state.App{}, nil, err
}

func projectTool(p api.ToolPublicationRecord, app state.App, name string) (api.ToolRegistryTool, error) {
	out := api.ToolRegistryTool{}
	if p.Outcome == nil || p.Status != "completed" {
		return out, state.ErrNotFound
	}
	catalog, err := mcphosting.ReadContract(bytes.NewReader(p.Outcome.Catalog))
	if err != nil {
		return out, err
	}
	for _, tool := range catalog.Tools {
		if tool.Name == name {
			projected, buildErr := mcphosting.NewCatalogContract(catalog.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{tool}})
			if buildErr != nil {
				return out, buildErr
			}
			raw, marshalErr := mcphosting.MarshalContract(projected)
			if marshalErr != nil {
				return out, marshalErr
			}
			return api.ToolRegistryTool{Version: p.ID, AccountID: p.AccountID, App: app.Slug, AppID: app.ID, Name: name, DeploymentID: p.Outcome.DeploymentID,
				SourceSHA256: p.Intent.SourceSHA256, DeploymentSourceSHA256: p.DeploymentSourceSHA256, CatalogSHA256: p.Outcome.CatalogSHA256,
				CatalogContractSHA256: toolpublication.Digest(raw), PublishedCatalogContractSHA256: toolpublication.Digest(p.Outcome.Catalog), Catalog: raw,
				AccessMode: p.Intent.AccessMode, RequiredScopes: append([]string{}, p.Intent.ToolScopes[name]...), EvidenceOrigin: p.EvidenceOrigin, Availability: "not_checked"}, nil
		}
	}
	return out, state.ErrNotFound
}
