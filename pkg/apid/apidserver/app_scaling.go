// adr: 20261104104521
package apidserver

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appscaling"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) appScalingHandler() httpkit.AccountHandler {
	store, _ := s.store.(state.AppScalingStore)
	return appscaling.Handler(appscaling.Deps{Store: store,
		Resolve: func(w http.ResponseWriter, r *http.Request, account state.Account, write bool) (state.App, string, bool) {
			app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
			if !ok {
				return state.App{}, "", false
			}
			app, env, problem := s.appEnvironmentSettings(w, r, account, app, write)
			if problem != nil {
				api.WriteProblem(w, problem)
				return state.App{}, "", false
			}
			scope := env.Slug
			if scope == "" {
				scope = "default"
			}
			return app, scope, true
		},
		Before: s.appUpdateSettingsProblem,
		Validate: func(account state.Account, app state.App, req api.UpdateAppRequest) *api.Problem {
			return validateUpdateApp(&req, account, api.MustLimitsFor(account.Plan), app)
		},
		After: func(r *http.Request, account state.Account, app state.App, receipt api.AppScalingReceipt) {
			_ = s.notif.Notify(r.Context(), db.NotifyAppChanged, fmt.Sprintf(`{"kind":"updated","slug":%q,"app_id":%q}`, app.Slug, app.ID))
			s.audit.Emit(r.Context(), "app.scaling", &account.ID, map[string]any{"app_id": app.ID, "scope": receipt.Scope, "request_id": receipt.RequestID, "revision": receipt.Revision})
			s.recordAppActivity(r.Context(), r, account, app, state.OrgActivity{Kind: "app.updated", ResourceType: "app", ResourceID: app.ID, ResourceLabel: app.Slug, SourceType: "app.scaling", SourceID: receipt.RequestID, Data: activityData(map[string]any{"scope": receipt.Scope, "revision": receipt.Revision, "settings": receipt.Settings})})
		},
	})
}
