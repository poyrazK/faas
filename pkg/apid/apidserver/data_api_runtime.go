package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/dataapiruntime"
	"github.com/onebox-faas/faas/pkg/state"
)

// appDataAPIRuntime serves the Data API runtime to the app's owner
// (ADR-20261031095954); the route prefix carries the data-api capability.
func (s *server) appDataAPIRuntime(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.loadApp(w, r, acct, r.PathValue("slug")); !ok {
		return
	}
	dataapiruntime.Serve(w, r)
}
