package appevents

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetEventRecoveryHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	if len(r.URL.Query()) != 0 {
		s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventRecoveryHealthStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery health"))
		return
	}
	out, err := store.GetEventRecoveryHealth(r.Context(), acct.ID, app.ID, time.Now().UTC())
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
