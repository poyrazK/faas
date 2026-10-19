package queueaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type TriggerStore interface {
	AppByID(context.Context, string) (state.App, error)
	OrgByID(context.Context, string) (state.Org, error)
	ListApps(context.Context, string) ([]state.App, error)
	TriggerByID(context.Context, string) (sqlc.Trigger, error)
	ListTriggersForApp(context.Context, string) ([]sqlc.Trigger, error)
}

// Legacy trigger administration is personal. Company consumers are accessible
// only through reviewed binding and queue routes, including for their creator.
func guardLegacyTrigger(w http.ResponseWriter, r *http.Request, store TriggerStore, actor state.Account) bool {
	if !strings.HasPrefix(r.URL.Path, "/v1/triggers") || r.Pattern == "GET /v1/triggers" {
		return true
	}
	appID := ""
	if id := r.PathValue("id"); id != "" {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return true
		} // Preserve the handler's malformed-ID validation.
		trigger, err := store.TriggerByID(r.Context(), parsed.String())
		if err != nil {
			api.WriteProblem(w, problem(state.ErrNotFound))
			return false
		}
		appID = trigger.AppID.String()
	} else if r.Method == http.MethodPost {
		var ok bool
		appID, ok = triggerBodyApp(w, r)
		if !ok {
			return false
		}
	}
	if appID == "" {
		return true
	}
	app, err := store.AppByID(r.Context(), appID)
	if err != nil {
		if r.Method == http.MethodPost && r.PathValue("id") == "" {
			return true
		} // Preserve existing request and plan validation.
		api.WriteProblem(w, problem(state.ErrNotFound))
		return false
	}
	allowed, err := personalTriggerApp(r, store, actor, app)
	if err != nil {
		api.WriteProblem(w, problem(err))
		return false
	}
	if !allowed {
		api.WriteProblem(w, problem(state.ErrNotFound))
		return false
	}
	return true
}

func triggerBodyApp(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Body == nil {
		return "", true
	}
	previous := r.Body
	body, err := io.ReadAll(http.MaxBytesReader(w, previous, httpkit.DefaultJSONBodyMaxBytes))
	_ = previous.Close()
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("could not read trigger request"))
		return "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var target struct {
		AppID string `json:"app_id"`
	}
	if json.Unmarshal(body, &target) != nil {
		return "", true
	} // The handler preserves its existing JSON validation.
	return target.AppID, true
}

func personalTriggerApp(r *http.Request, store TriggerStore, actor state.Account, app state.App) (bool, error) {
	if app.AccountID != actor.ID {
		return false, nil
	}
	_, key, _ := authmw.AccountFromContext(r)
	if key != nil && key.AppID != "" && key.AppID != app.ID {
		return false, nil
	}
	if app.OrgID == "" {
		if key == nil || key.OrgID == "" {
			return true, nil
		}
		org, err := store.OrgByID(r.Context(), key.OrgID)
		return err == nil && org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == actor.ID, err
	}
	org, err := store.OrgByID(r.Context(), app.OrgID)
	if err != nil {
		return false, err
	}
	return org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == actor.ID && (key == nil || key.OrgID == "" || key.OrgID == org.ID), nil
}

// ListPersonalTriggers runs inside the route's MFA and scope checks. Filtering
// happens before serialization, so account inventories cannot reveal company
// projections or a different app's triggers through an app-scoped credential.
func ListPersonalTriggers(store TriggerStore, project func(sqlc.Trigger) api.Trigger) authmw.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, actor state.Account) {
		apps, err := personalTriggerApps(r, store, actor)
		if err != nil {
			api.WriteProblem(w, problem(err))
			return
		}
		out := []api.Trigger{}
		kind := r.URL.Query().Get("kind")
		for _, app := range apps {
			rows, err := store.ListTriggersForApp(r.Context(), app.ID)
			if err != nil {
				api.WriteProblem(w, problem(err))
				return
			}
			for _, row := range rows {
				if kind == "" || row.Kind == kind {
					out = append(out, project(row))
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func personalTriggerApps(r *http.Request, store TriggerStore, actor state.Account) ([]state.App, error) {
	_, key, _ := authmw.AccountFromContext(r)
	if key != nil && key.OrgID != "" {
		org, err := store.OrgByID(r.Context(), key.OrgID)
		if err != nil {
			return nil, err
		}
		if !org.Personal {
			return nil, state.ErrNotFound
		}
	}
	appID := r.URL.Query().Get("app_id")
	if key != nil && key.AppID != "" {
		if appID != "" && appID != key.AppID {
			return nil, state.ErrNotFound
		}
		appID = key.AppID
	}
	if appID != "" {
		app, err := store.AppByID(r.Context(), appID)
		if err != nil {
			return nil, state.ErrNotFound
		}
		allowed, err := personalTriggerApp(r, store, actor, app)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, state.ErrNotFound
		}
		return []state.App{app}, nil
	}
	apps, err := store.ListApps(r.Context(), actor.ID)
	if err != nil {
		return nil, err
	}
	out := []state.App{}
	for _, app := range apps {
		allowed, err := personalTriggerApp(r, store, actor, app)
		if err != nil {
			return nil, err
		}
		if allowed {
			out = append(out, app)
		}
	}
	return out, nil
}
