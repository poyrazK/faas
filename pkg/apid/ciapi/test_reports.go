package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) TestReports(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, row, ok := h.ownedExecution(w, r, acct)
	if !ok {
		return
	}
	reports, err := ci.ExecutionReports(row)
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, reports)
}
