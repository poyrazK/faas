// Package toolregistry owns private publication journals and their immutable
// registry projection. It never executes customer code or resumes publishers.
package toolregistry

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func Handler(store state.Store) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		if !personalAccess(w, r, acct, store, true) {
			return
		}
		journal, ok := store.(state.ToolPublicationStore)
		if !ok {
			respond(w, nil, errors.New("publication store unavailable"))
			return
		}
		id := r.PathValue("id")
		if !toolpublication.ValidID(id) {
			api.WriteProblem(w, api.ErrValidation("publication id must be 64 lowercase hex characters"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			out, err := journal.GetToolPublication(r.Context(), acct.ID, id)
			respond(w, out, err)
		case http.MethodPut:
			begin(w, r, acct, store, journal, id)
		case http.MethodPost:
			finish(w, r, acct, store, journal, id)
		}
	}
}

// This first registry is personal-account only. Scoped principals must not
// inherit account-wide catalogs through this new surface.
func accountAccess(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store) bool {
	return personalAccess(w, r, acct, store, false)
}

// Publishing keys may use the account publication journal, but still discover
// and invoke through exact owner grants. Deployment scope does not open the
// registry, grants or owner invocation routes.
func personalAccess(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, publishing bool) bool {
	p, ok := httpkit.PrincipalFrom(r)
	denied := !ok || p.Acct.ID != acct.ID || r.Header.Get("X-Active-Org") != "" || r.URL.Query().Get("org") != ""
	if ok && p.Key != nil {
		k := p.Key
		publisher := publishing && slices.Contains(k.Scopes, api.ScopeDeployWrite)
		denied = denied || k.AppID != "" || k.ProjectLimited || k.PlatformTenantID != "" || k.HostingSiteID != "" || agentRegistryRequest(r) && !publisher
		if k.OrgID != "" {
			personal, err := store.OrgByPersonalAccount(r.Context(), acct.ID)
			denied = denied || err != nil || k.OrgID != personal.ID
		}
	}
	if denied {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Account connection required", "private tool records require an unscoped personal-account connection"))
	}
	return !denied
}

func begin(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, journal state.ToolPublicationStore, id string) {
	var in api.ToolPublicationIntent
	if httpkit.DecodeJSONSized(r, &in, api.ToolPublicationIntentMaxBytes) != nil || toolpublication.ValidateIntent(in) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid publication review"))
		return
	}
	// Existing records are immutable. Re-reading one does not require its old
	// deployment to remain serving, and never returns permission to execute.
	_, err := journal.GetToolPublication(r.Context(), acct.ID, id)
	if err == nil {
		out, err := journal.BeginToolPublication(r.Context(), acct.ID, id, in)
		respond(w, out, err)
		return
	}
	if !errors.Is(err, state.ErrNotFound) {
		respond(w, nil, err)
		return
	}
	app, err := store.AppBySlug(r.Context(), in.App)
	if errors.Is(err, state.ErrNotFound) && in.AppID == "" && in.ExpectedServingDeploymentID == "" {
		out, err := journal.BeginToolPublication(r.Context(), acct.ID, id, in)
		respond(w, out, err)
		return
	}
	if err != nil || !personalApp(r, acct, store, app) || app.ID != in.AppID {
		respond(w, nil, state.ErrConflict)
		return
	}
	out, err := journal.BeginToolPublication(r.Context(), acct.ID, id, in)
	respond(w, out, err)
}

func finish(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, journal state.ToolPublicationStore, id string) {
	var in api.ToolPublicationOutcome
	if httpkit.DecodeJSONSized(r, &in, api.ToolPublicationOutcomeMaxBytes) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid publication evidence"))
		return
	}
	record, err := journal.GetToolPublication(r.Context(), acct.ID, id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if toolpublication.ValidateOutcome(record.Intent, in) != nil || (in.Status == "completed" && !validCatalog(in.Catalog)) {
		api.WriteProblem(w, api.ErrValidation("evidence does not match the reviewed catalog and tests"))
		return
	}
	source := ""
	if in.Status == "completed" {
		source, err = deploymentSource(r, acct, store, record, in)
		if err != nil {
			respond(w, nil, err)
			return
		}
	}
	out, err := journal.FinishToolPublication(r.Context(), acct.ID, id, in, source)
	respond(w, out, err)
}

func deploymentSource(r *http.Request, acct state.Account, store state.Store, record api.ToolPublicationRecord, in api.ToolPublicationOutcome) (string, error) {
	dep, err := store.DeploymentByID(r.Context(), in.DeploymentID)
	if err != nil {
		return "", state.ErrNotFound
	}
	app, err := store.AppByID(r.Context(), dep.AppID)
	if err != nil || !personalApp(r, acct, store, app) || app.Slug != record.Intent.App || (record.Intent.AppID != "" && app.ID != record.Intent.AppID) {
		return "", state.ErrNotFound
	}
	if !toolpublication.ValidID(dep.SourceSHA256) || (dep.Scope != "" && dep.Scope != api.DefaultEnvScope) {
		return "", state.ErrInvalidArgument
	}
	// Repeated finalization can retrieve immutable evidence after later releases.
	if record.Status == "running" && (dep.Status != state.DeployLive || dep.TrafficPercent != 100 || !time.Now().Before(record.Deadline)) {
		return "", state.ErrConflict
	}
	return dep.SourceSHA256, nil
}

func personalApp(r *http.Request, acct state.Account, store state.Store, app state.App) bool {
	if app.AccountID != acct.ID {
		return false
	}
	if app.OrgID == "" {
		return true
	}
	org, err := store.OrgByPersonalAccount(r.Context(), acct.ID)
	return err == nil && org.ID == app.OrgID
}

func Search(store state.Store) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		if agentRegistryRequest(r) {
			scopedSearch(w, r, acct, store)
			return
		}
		if !accountAccess(w, r, acct, store) {
			return
		}
		journal, ok := store.(state.ToolPublicationStore)
		if !ok {
			respond(w, nil, errors.New("publication store unavailable"))
			return
		}
		q := r.URL.Query()
		limit := api.ToolRegistryPageMax
		var err error
		if q.Has("limit") {
			limit, err = strconv.Atoi(q.Get("limit"))
		}
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid registry page limit"))
			return
		}
		page, err := journal.SearchToolRegistry(r.Context(), acct.ID, q.Get("query"), q.Get("cursor"), limit)
		out := api.ToolRegistryPage{Versions: []api.ToolRegistryVersion{}, NextCursor: page.NextCursor}
		for _, record := range page.Publications {
			out.Versions = append(out.Versions, toolpublication.Version(record))
		}
		respond(w, out, err)
	}
}

func respond(w http.ResponseWriter, out any, err error) {
	var invocationLimit *state.ToolInvocationLimitError
	switch {
	case errors.As(err, &invocationLimit):
		api.WriteProblem(w, api.NewProblem(429, "tool_invocation_limit", "Invocation capacity reached", "the preview bounds active calls and retains invocation identities to prevent replay").WithLimit(invocationLimit.Limit, invocationLimit.Observed).WithDocs("https://gregale.dev/docs/mcp#invoke-published-tools"))
	case errors.Is(err, state.ErrToolPublicationLimit):
		api.WriteProblem(w, api.NewProblem(429, "tool_publication_limit", "Publication journal is full", "the preview retains failed and abandoned identities to prevent replay").WithLimit(api.ToolPublicationsPerAccount, api.ToolPublicationsPerAccount+1).WithDocs("https://gregale.dev/docs/mcp#private-tool-registry"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Tool publication not found", "no matching record belongs to this account"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, api.CodeValidation, "Publication changed", "publication evidence is immutable; inspect the retained record and native deployment history"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid publication evidence or registry query"))
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("tool publication evidence unavailable"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
