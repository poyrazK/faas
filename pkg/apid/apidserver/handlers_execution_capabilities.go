package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/executionprofiles"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getExecutionCapabilities(w http.ResponseWriter, _ *http.Request, acct state.Account) {
	writeJSON(w, http.StatusOK, executionprofiles.Capabilities(acct.Plan, s.executionAPIEnabled))
}
