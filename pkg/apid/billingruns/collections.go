package billingruns

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) Collections(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, _, ok := s.completedDraftRun(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.BillingRunCollectionsStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("billing run payments are unavailable"))
		return
	}
	totals, err := store.BillingRunCollections(r.Context(), acct.ID, run.ID, s.now())
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read billing run payments"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, api.BillingRunCollectionsResponse{Totals: totals})
}
