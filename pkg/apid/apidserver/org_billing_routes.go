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
