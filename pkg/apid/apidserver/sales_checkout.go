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
	actor, key, principal := authmw.AccountFromContext(r)
	_, session := sessionFrom(r)
	if !principal || key != nil || !session {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Session required", "Sign in with a browser session to use a salesperson code."))
		return false
	}
	if problem := s.sales.PrepareCheckout(r.Context(), actor, acct, plan, code); problem != nil {
		api.WriteProblem(w, problem)
		return false
	}
	return true
}

// orgReferralCheckout serves GET /v1/orgs/{slug}/referrals/checkout: whether
// the organization's first paid purchase can still take a salesperson code.
func (s *server) orgReferralCheckout(w http.ResponseWriter, r *http.Request, subject state.Account) {
	actor, key, principal := authmw.AccountFromContext(r)
	if !principal || key != nil {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Session required", "Sign in with a browser session to use a salesperson code."))
		return
	}
	s.sales.OrgCheckoutStatus(w, r, actor, subject)
}
