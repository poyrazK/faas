package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/responsecheck"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) checkDeploymentResponse(w http.ResponseWriter, r *http.Request, account state.Account) {
	(responsecheck.Service{LoadApp: s.loadApp, Store: s.store, GatewayURL: s.gatewayControlURL(r.Context())}).Check(w, r, account)
}

func (s *server) getDeploymentResponseCheckCapabilities(w http.ResponseWriter, r *http.Request, _ state.Account) {
	(responsecheck.Service{GatewayURL: s.gatewayControlURL(r.Context())}).ServeCapabilities(w, r)
}
