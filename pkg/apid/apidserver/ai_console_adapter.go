package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/aiconsole"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// aiConsoleHost serves the AI gateway's dashboard pages
// (ADR 20261022145950).
type aiConsoleHost struct{ server *server }

func (h aiConsoleHost) Store() state.Store { return h.server.store }

// Gate is aiGatewayDenied's check as a sentence for the page.
func (h aiConsoleHost) Gate(acct state.Account) string {
	switch {
	case !h.server.platformFlagOn(platformflags.AIGateway):
		return "The AI gateway is not enabled on this platform."
	case !h.server.platformFlagEnabled(platformflags.AIGateway, acct):
		return "The AI gateway preview is not enabled for this account yet."
	case !outbound.AIGatewayEntitled(acct.Plan):
		return "Your plan does not include the AI gateway."
	}
	return ""
}

func (h aiConsoleHost) Render(w http.ResponseWriter, r *http.Request, acct state.Account, page dashboard.Page) {
	page.Account = dashboardAccountView(acct, 0)
	if err := dashboard.Render(w, h.server.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, h.server.log, err)
	}
}

// aiConsoleHandler serves /dashboard/ai for the signed-in account.
func (s *server) aiConsoleHandler() http.Handler {
	console := &aiconsole.Service{Host: aiConsoleHost{server: s}, Log: s.log}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acct, ok := AccountFrom(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		console.Serve(w, r, acct)
	})
}
