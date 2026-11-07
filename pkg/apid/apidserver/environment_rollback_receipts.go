package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/environmentrollback"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEnvironmentRollbackReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	environmentrollback.Read(w, r, acct.ID, s.store)
}
