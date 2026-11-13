package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) prepareSalesCheckout(w http.ResponseWriter, r *http.Request, acct state.Account, plan api.Plan, code string) bool {
	if referrals.NormalizeCode(code) == "" {
		return true
	}
	_, key, principal := authmw.AccountFromContext(r)
	_, session := sessionFrom(r)
	if !principal || key != nil || !session {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Session required", "Sign in with a browser session to use a salesperson code."))
		return false
	}
	if problem := s.sales.PrepareCheckout(r.Context(), acct, plan, code); problem != nil {
		api.WriteProblem(w, problem)
		return false
	}
	return true
}
