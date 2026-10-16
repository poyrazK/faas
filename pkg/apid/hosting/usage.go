package hosting

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s Service) Usage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.guard(w, acct) {
		return
	}
	usage, err := s.Host.HostingStore().GetHostingUsage(r.Context(), acct.ID)
	if err != nil {
		failure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, usage)
}
