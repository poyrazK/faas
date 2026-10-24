package appaccess

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// These legacy resource URLs retain their creator-only permission boundary,
// but an app's creator must still be a current owner/admin of its company.
// An indirect URL never bypasses offboarding or grants a new shared action.
func indirectlyAddressedApp(r *http.Request, store Store, actorID string) (state.App, bool, error) {
	var appID string
	var err error
	path, id := r.URL.Path, r.PathValue("id")
	switch {
	case strings.HasPrefix(path, "/v1/domains/") && r.PathValue("domain") != "":
		var domain state.CustomDomain
		domain, err = store.DomainByName(r.Context(), strings.ToLower(r.PathValue("domain")))
		appID = domain.AppID
	case strings.HasPrefix(path, "/v1/crons/") && id != "":
		var cron state.Cron
		cron, err = store.CronByID(r.Context(), id)
		appID = cron.AppID
	case strings.HasPrefix(path, "/v1/edge-rules/") && id != "":
		var rule state.EdgeRule
		rule, err = store.GetEdgeRuleByID(r.Context(), id)
		appID = rule.AppID
	case strings.HasPrefix(path, "/v1/invocations/") && id != "":
		var invocation state.Invocation
		invocation, err = store.InvocationByID(r.Context(), id)
		appID = invocation.AppID
	default:
		appID, err = ownedResourceApp(r, store, actorID)
		if appID == "" && err == nil {
			return state.App{}, false, nil
		}
	}
	if err != nil {
		return state.App{}, true, err
	}
	if appID == "" {
		return state.App{}, false, nil // Account-scoped rules have no app.
	}
	app, err := store.AppByID(r.Context(), appID)
	return app, true, err
}

// ownedResourceApp resolves the app behind resource URLs whose handlers check
// only that the caller created the app. Their rows stay under the creator's
// account after the creator leaves the company, so without this lookup a
// removed or demoted creator keeps acting on the company app through them.
// A missing resource is left to its handler, which answers with the error it
// documents; the lookup never reveals another account's resource. Freezes,
// operations and commit sources use the optional stores their handlers use.
func ownedResourceApp(r *http.Request, store Store, actorID string) (string, error) {
	ctx, path, id := r.Context(), r.URL.Path, r.PathValue("id")
	switch {
	case strings.HasPrefix(path, "/v1/triggers/") && validID(id):
		trigger, err := store.TriggerByID(ctx, id)
		if err != nil || !trigger.AppID.Valid {
			return "", missing(err)
		}
		return uuid.UUID(trigger.AppID.Bytes).String(), nil
	case strings.HasPrefix(path, "/v1/workflows/runs/") && validID(id):
		run, err := store.GetWorkflowRun(ctx, id)
		if err != nil || run == nil {
			return "", missing(err)
		}
		return run.AppID, nil
	case strings.HasPrefix(path, "/v1/delayed-tasks/") && validID(id):
		invocation, err := store.InvocationByID(ctx, id)
		return invocation.AppID, missing(err)
	case strings.HasPrefix(path, "/v1/deleted-apps/") && validID(id):
		return id, nil
	case strings.HasPrefix(path, "/v1/preview/") && r.PathValue("slug") != "":
		preview, err := store.AppBySlug(ctx, r.PathValue("slug"))
		return preview.ID, missing(err)
	case strings.HasPrefix(path, "/v1/freezes/") && validID(id):
		freezes, ok := store.(state.ReleaseFreezeStore)
		if !ok {
			return "", nil
		}
		rows, err := freezes.ListReleaseFreezes(ctx, actorID, time.Now().UTC())
		for _, row := range rows {
			if row.ID == id {
				return row.AppID, nil
			}
		}
		return "", missing(err)
	case strings.HasPrefix(path, "/v1/operations/") && validID(id):
		operations, ok := store.(state.ExclusiveWorkStore)
		if !ok {
			return "", nil
		}
		operation, err := operations.ExclusiveOperationByID(ctx, actorID, id)
		return operation.AppID, missing(err)
	case strings.HasPrefix(path, "/v1/commit-sources/") && r.PathValue("source") != "":
		sources, ok := store.(state.CommitStore)
		if !ok {
			return "", nil
		}
		source, err := sources.CommitSourceByID(ctx, actorID, r.PathValue("source"))
		return source.AppID, missing(err)
	}
	return "", nil
}

func validID(id string) bool {
	_, err := uuid.Parse(id)
	return id != "" && err == nil
}

// missing hides a not-found lookup so the handler keeps its own response.
func missing(err error) error {
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrWorkflowRunNotFound) {
		return nil
	}
	return err
}
