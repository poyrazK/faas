package toolregistry

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func Invocations(store state.Store) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		if !accountAccess(w, r, acct, store) {
			return
		}
		journal, ok := store.(state.ToolInvocationStore)
		if !ok {
			respond(w, nil, errors.New("invocation journal unavailable"))
			return
		}
		id := r.PathValue("id")
		if !toolinvocation.ValidID(id) {
			api.WriteProblem(w, api.ErrValidation("invocation id must be a canonical nonzero UUID"))
			return
		}
		if r.Method == http.MethodPut {
			beginInvocation(w, r, acct, store, journal, id)
			return
		}
		if r.Method == http.MethodGet {
			record, err := journal.GetToolInvocation(r.Context(), acct.ID, id)
			respond(w, record, err)
			return
		}
		changeInvocation(w, r, acct, store, journal, id)
	}
}

func beginInvocation(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, journal state.ToolInvocationStore, id string) {
	var in api.ToolInvocationIntent
	if httpkit.DecodeJSONSized(r, &in, api.ToolInvocationRequestMaxBytes) != nil || toolinvocation.ValidateIntent(in) != nil {
		api.WriteProblem(w, api.ErrValidation("exact version, argument digest and explicit execution consent are required"))
		return
	}
	old, err := journal.GetToolInvocation(r.Context(), acct.ID, id)
	if err == nil {
		if !reflect.DeepEqual(old.Intent, in) {
			respond(w, nil, state.ErrConflict)
			return
		}
		respond(w, api.ToolInvocationReceipt{Record: old}, nil)
		return
	}
	if !errors.Is(err, state.ErrNotFound) {
		respond(w, nil, err)
		return
	}
	publications, ok := store.(state.ToolPublicationStore)
	if !ok {
		respond(w, nil, errors.New("publication journal unavailable"))
		return
	}
	publication, err := publications.GetToolPublication(r.Context(), acct.ID, in.Version)
	if err != nil {
		respond(w, nil, err)
		return
	}
	target, err := invocationTarget(r, acct, store, publication, in)
	if err != nil {
		respond(w, nil, err)
		return
	}
	out, err := journal.BeginToolInvocation(r.Context(), acct.ID, id, in, target)
	respond(w, out, err)
}

func invocationTarget(r *http.Request, acct state.Account, store state.Store, p api.ToolPublicationRecord, in api.ToolInvocationIntent) (api.ToolInvocationTarget, error) {
	var target api.ToolInvocationTarget
	if p.Status != "completed" || p.Outcome == nil || p.Outcome.Status != "completed" || p.Intent.AccessMode != in.AccessMode || !toolpublication.ValidEndpointPath(p.Intent.EndpointPath) {
		return target, state.ErrInvalidArgument
	}
	if _, ok := p.Intent.ToolScopes[in.Name]; !ok {
		return target, state.ErrInvalidArgument
	}
	if toolpublication.ValidateOutcome(p.Intent, *p.Outcome) != nil || !validCatalog(p.Outcome.Catalog) {
		return target, state.ErrInvalidArgument
	}
	dep, err := store.DeploymentByID(r.Context(), p.Outcome.DeploymentID)
	if err != nil {
		return target, state.ErrNotFound
	}
	app, err := store.AppByID(r.Context(), dep.AppID)
	if err != nil || !personalApp(r, acct, store, app) || app.Slug != p.Intent.App || p.Intent.AppID != "" && p.Intent.AppID != app.ID {
		return target, state.ErrNotFound
	}
	target = api.ToolInvocationTarget{App: app.Slug, AppID: app.ID, DeploymentID: dep.ID, DeploymentSourceSHA256: p.DeploymentSourceSHA256,
		CatalogSHA256: p.Outcome.CatalogSHA256, CatalogContractSHA256: toolpublication.Digest(p.Outcome.Catalog), EndpointPath: p.Intent.EndpointPath}
	if err = availableInvocationTarget(r, acct, store, target); err != nil {
		return target, err
	}
	return target, nil
}

func availableInvocationTarget(r *http.Request, acct state.Account, store state.Store, target api.ToolInvocationTarget) error {
	dep, err := store.DeploymentByID(r.Context(), target.DeploymentID)
	if err != nil {
		return state.ErrNotFound
	}
	app, err := store.AppByID(r.Context(), dep.AppID)
	if err != nil || !personalApp(r, acct, store, app) || app.ID != target.AppID || app.Slug != target.App {
		return state.ErrNotFound
	}
	var overrides map[string]string
	if json.Unmarshal(dep.OverrideEnv, &overrides) != nil || overrides["MCP_CATALOG_EXPECTED_SHA256"] != target.CatalogSHA256 || dep.SourceSHA256 != target.DeploymentSourceSHA256 ||
		dep.Status != state.DeployLive || dep.Scope != "" && dep.Scope != api.DefaultEnvScope {
		return state.ErrConflict
	}
	return nil
}

func changeInvocation(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, journal state.ToolInvocationStore, id string) {
	action := r.PathValue("action")
	record, err := journal.GetToolInvocation(r.Context(), acct.ID, id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if record.Delegation != nil && action != "cancel" {
		respond(w, nil, state.ErrConflict)
		return
	}
	if action == "dispatch" && record.Status == "queued" {
		if err = availableInvocationTarget(r, acct, store, record.Target); err != nil {
			respond(w, nil, err)
			return
		}
	}
	var outcome *api.ToolInvocationOutcome
	if action == "outcome" {
		var ok bool
		outcome, ok = readInvocationOutcome(w, r)
		if !ok {
			return
		}
		action = "finish"
	} else if action != "dispatch" && action != "cancel" {
		api.WriteProblem(w, api.ErrValidation("unknown invocation action"))
		return
	}
	out, err := journal.ChangeToolInvocation(r.Context(), acct.ID, id, action, outcome)
	respond(w, out, err)
}
