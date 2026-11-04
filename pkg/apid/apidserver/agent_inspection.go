// adr: 20261103211147
package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agentinspect"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) agentInspection() agentinspect.Deps {
	reader, _ := s.store.(state.AppResourceInventoryReader)
	return agentinspect.Deps{Store: s.store, Inventory: func(r *http.Request, q state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error) {
		if q.Kind == "databases" && s.managedPostgresBindings != nil {
			return s.managedPostgresBindings.ResourceInventory(r.Context(), q)
		}
		if reader == nil {
			return state.AppResourceInventoryPage{}, state.ErrAppResourceInventoryUnavailable
		}
		return reader.ListAppResourceInventory(r.Context(), q)
	}, Resolve: func(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ProjectEnvironment, bool) {
		app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
		if !ok {
			return app, state.ProjectEnvironment{}, false
		}
		app, env, p := s.appEnvironmentSettings(w, r, acct, app, false)
		if p != nil {
			api.WriteProblem(w, p)
			return app, env, false
		}
		return app, env, true
	}, Released: func(r *http.Request, acct state.Account, app state.App, pattern string) bool {
		return s.capGate == nil || s.capGate.Allowed(pattern, appFlagSubject(acct, app))
	}}
}
