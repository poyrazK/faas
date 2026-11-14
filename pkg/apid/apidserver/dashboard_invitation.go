package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
)

// The CLI hands invitations to this same-origin page so a browser session,
// rather than the CLI's bearer credential, proves who is accepting.
func (s *server) dashboardInvitation(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	page := dashboard.Page{
		Title: "Organization invitation", Body: "invitation", Account: acctViewFrom(acct),
		Data: r.PathValue("token"),
	}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}
