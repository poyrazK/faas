package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/responsecheck"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) checkDeploymentResponse(w http.ResponseWriter, r *http.Request, account state.Account) {
	(responsecheck.Service{LoadApp: s.loadApp, Store: s.store, GatewayURL: s.gatewaydControlURL}).Check(w, r, account)
}
