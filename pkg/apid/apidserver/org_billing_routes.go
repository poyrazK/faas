package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/orgbilling"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) orgBillingRoute(action authz.OrgAction, next accountHandler) http.HandlerFunc {
	scoped := orgbilling.Scope(s.store, s.audit, action, authmw.AccountHandler(next))
	scopes := api.ScopesUsageReadSurface
	if action == authz.OrgActionChangePlan {
		scopes = api.ScopesAdminOnly
	}
	wrapped := accountHandler(scoped)
	if action == authz.OrgActionChangePlan {
		wrapped = s.requireVerifiedEmail(s.requireStepUp(5 * time.Minute)(s.idempotent(wrapped)))
	}
	return s.authLimited(s.requireMFA(s.requireScope(scopes...)(s.loadOrg(wrapped))))
}

// orgFinancialBudgetRoute serves an organization's costs and budgets with the
// account handlers: orgbilling.Scope hands them the organization's billing
// subject, which owns the budgets and the usage they measure
// (ADR-20261031211500). Every member may read them; writes need
// org.manage_billing (owner or billing), an admin-scoped credential and an
// Idempotency-Key, as an account's budget writes do.
func (s *server) orgFinancialBudgetRoute(write bool, next accountHandler) http.HandlerFunc {
	action, scopes := authz.OrgActionView, api.ScopesUsageReadSurface
	if write {
		action, scopes = authz.OrgActionManageBilling, api.ScopesAdminOnly
	}
	wrapped := accountHandler(orgbilling.Scope(s.store, s.audit, action, authmw.AccountHandler(next)))
	if write {
		wrapped = s.requireFinancialBudgetIdempotency(wrapped)
	}
	return s.authLimited(s.requireMFA(s.requireScope(scopes...)(s.loadOrg(wrapped))))
}

func (s *server) changeOrgPlan(w http.ResponseWriter, r *http.Request, target state.Account) {
	actor, _, _, ok := authmw.PrincipalFrom(r)
	if !ok || !actor.EmailVerified() {
		api.WriteProblem(w, api.ErrEmailVerificationRequired())
		return
	}
	fresh, err := orgbilling.BillingContact(r.Context(), s.store, target, actor)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not save organization billing contact"))
		return
	}
	s.changePlan(w, r, fresh)
}
