package appaccess

import (
	"context"
	"errors"
	"net/http"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// VisibleApps keeps creator-account inventories from revealing company apps
// after the creator has left that company. Accounting ownership is unchanged.
func VisibleApps(r *http.Request, store Store, actor state.Account, apps []state.App) ([]state.App, error) {
	return VisibleAppsContext(r.Context(), store, actor, apps)
}

// VisibleAppsContext is VisibleApps for account views that resolve apps from
// the request's context: dashboards and incidents read app data without
// addressing the app in their URL, so Wrap never decides for them.
func VisibleAppsContext(ctx context.Context, store Store, actor state.Account, apps []state.App) ([]state.App, error) {
	_, key, _ := authmw.AccountFromCtx(ctx)
	visible := make([]state.App, 0, len(apps))
	for _, app := range apps {
		if key != nil && (key.AppID != "" && key.AppID != app.ID || key.OrgID != "" && app.OrgID != "" && key.OrgID != app.OrgID) {
			continue
		}
		if app.OrgID == "" && (app.AccountID != actor.ID || key != nil && key.OrgID != "" && !personalKey(ctx, store, key.OrgID, actor.ID)) {
			continue
		}
		if app.OrgID != "" {
			org, err := store.OrgByID(ctx, app.OrgID)
			if err != nil {
				return nil, err
			}
			if org.Personal && (app.AccountID != actor.ID || org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != actor.ID) {
				continue
			}
			if !org.Personal {
				member, err := store.OrgAccessSnapshot(ctx, org.ID, actor.ID)
				if errors.Is(err, state.ErrNotFound) {
					continue
				}
				if err != nil {
					return nil, err
				}
				if org.DeletedPending || org.Status == state.OrgStatusDeletedPending {
					continue
				}
				switch member.Role {
				case state.OrgRoleOwner, state.OrgRoleAdmin, state.OrgRoleDeveloper, state.OrgRoleViewer:
				default:
					continue
				}
			}
		}
		visible = append(visible, app)
	}
	return visible, nil
}

func VisibleBuilds(r *http.Request, store Store, actor state.Account, builds []state.Build) ([]state.Build, error) {
	apps := make(map[string]state.App)
	buildApps := make(map[string]string, len(builds))
	for _, build := range builds {
		dep, err := store.DeploymentByID(r.Context(), build.DeploymentID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, found := apps[dep.AppID]; !found {
			app, err := store.AppByID(r.Context(), dep.AppID)
			if err != nil {
				return nil, err
			}
			apps[app.ID] = app
		}
		buildApps[build.ID] = dep.AppID
	}
	all := make([]state.App, 0, len(apps))
	for _, app := range apps {
		all = append(all, app)
	}
	visible, err := VisibleApps(r, store, actor, all)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(visible))
	for _, app := range visible {
		allowed[app.ID] = true
	}
	out := make([]state.Build, 0, len(builds))
	for _, build := range builds {
		if allowed[buildApps[build.ID]] {
			out = append(out, build)
		}
	}
	return out, nil
}
