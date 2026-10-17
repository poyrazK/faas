package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	liveapi "github.com/onebox-faas/faas/pkg/apid/automationlive"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) liveAutomationService(w http.ResponseWriter, r *http.Request, account state.Account) (liveapi.Service, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return liveapi.Service{}, "", false
	}
	store, ok := s.store.(state.AutomationLiveStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Live verification is unavailable."))
		return liveapi.Service{}, "", false
	}
	return liveapi.Service{Store: store, RuntimeEnabled: s.workflowRuntimeEnabled}, app.ID, true
}

func (s *server) startAutomationLiveVerification(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveAutomationService(w, r, account); ok {
		service.Start(w, r, appID)
	}
}

func (s *server) listAutomationLiveVerifications(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveAutomationService(w, r, account); ok {
		service.List(w, r, appID)
	}
}

func (s *server) getAutomationLiveVerification(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveAutomationService(w, r, account); ok {
		service.Get(w, r, appID)
	}
}

func (s *server) checkAutomationLiveVerification(w http.ResponseWriter, r *http.Request, account state.Account) {
	if service, appID, ok := s.liveAutomationService(w, r, account); ok {
		service.Check(w, r, appID)
	}
}
