// adr: 1302
package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	liveapi "github.com/onebox-faas/faas/pkg/apid/automationlive"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) liveBaselineService(w http.ResponseWriter, r *http.Request, account state.Account) (liveapi.BaselineService, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return liveapi.BaselineService{}, "", false
	}
	store, ok := s.store.(state.AutomationLiveBaselineStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Named automation baselines are unavailable."))
		return liveapi.BaselineService{}, "", false
	}
	return liveapi.BaselineService{Store: store}, app.ID, true
}
func (s *server) getAutomationLiveBaseline(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveBaselineService(w, r, account); ok {
		service.Get(w, r, appID)
	}
}
func (s *server) getAutomationLiveBaselineHistory(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveBaselineService(w, r, account); ok {
		service.History(w, r, appID)
	}
}
func (s *server) promoteAutomationLiveBaseline(w http.ResponseWriter, r *http.Request, account state.Account) {
	service, appID, ok := s.liveBaselineService(w, r, account)
	if !ok {
		return
	}
	keyID := ""
	if _, key, authenticated := authmw.AccountFromContext(r); authenticated && key != nil {
		keyID = key.ID
	}
	service.Promote(w, r, appID, account.ID, keyID)
}

func (s *server) listAutomationLiveBaselines(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveBaselineService(w, r, account); ok {
		service.List(w, r, appID)
	}
}

func (s *server) retireAutomationLiveBaseline(w http.ResponseWriter, r *http.Request, account state.Account) {
	service, appID, ok := s.liveBaselineService(w, r, account)
	if !ok {
		return
	}
	keyID := ""
	if _, key, authenticated := authmw.AccountFromContext(r); authenticated && key != nil {
		keyID = key.ID
	}
	service.Retire(w, r, appID, account.ID, keyID)
}
