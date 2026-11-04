package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/orgbilling"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/logsanitize"
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

// putOrgBillingEmail moves the organization's billing email, and its billing
// provider customer with it, so invoices and the billing portal follow the
// organization rather than the member who first paid (ADR-20261103131742).
// The provider moves first: a failed provider update leaves both unchanged.
//
// Mounted at PUT /v1/orgs/{slug}/billing/email.
func (s *server) putOrgBillingEmail(w http.ResponseWriter, r *http.Request, target state.Account) {
	var req api.OrgBillingEmailRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	email, ok := orgbilling.NormalizeBillingEmail(req.BillingEmail)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid billing email", "billing_email must be one email address, such as billing@example.com"))
		return
	}
	writer, ok := s.store.(interface {
		UpdateOrgBillingEmail(context.Context, string, string) error
	})
	if !ok {
		api.WriteProblem(w, api.ErrInternal("organization billing email unavailable"))
		return
	}
	if err := s.moveBillingCustomerEmail(r.Context(), target, email); err != nil {
		if errors.Is(err, billing.ErrCustomerEmailInUse) {
			api.WriteProblem(w, api.ErrBillingEmailInUse(true))
			return
		}
		s.log.Error("move billing customer email", "org", target.ResourceOrgID, "err", logsanitize.FieldAny(err))
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "billing_email_change_failed",
			"billing email change failed", "the billing provider could not change the email; the billing email is unchanged"))
		return
	}
	if err := writer.UpdateOrgBillingEmail(r.Context(), target.ResourceOrgID, email); err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the organization billing email"))
		return
	}
	s.audit.Emit(r.Context(), "org.billing_email_changed", &target.ID, map[string]any{
		"org_id": target.ResourceOrgID,
	})
	writeJSON(w, http.StatusOK, api.OrgBillingEmailResponse{BillingEmail: email})
}

// moveBillingCustomerEmail moves the subject's billing provider customer to
// email. A subject without a provider customer has nothing to move: its first
// checkout creates the customer with the saved email.
func (s *server) moveBillingCustomerEmail(ctx context.Context, target state.Account, email string) error {
	updater, ok := s.billingProvider.(billing.CustomerEmailUpdater)
	if !ok || s.billingProvider == nil {
		return nil
	}
	billingAcct, err := s.accountForActiveBillingProvider(ctx, target)
	if err != nil {
		return err
	}
	if billingAcct.ProviderCustomerID == "" {
		return nil
	}
	billingAcct.Email = email
	return updater.UpdateCustomerEmail(ctx, billingAcct)
}

// refuseBillingContactRemoval answers 409 and reports true when the member
// being removed is the email the organization bills to while a billing
// provider customer exists: the removed member would keep receiving the
// organization's invoices and signing in to its billing portal.
func (s *server) refuseBillingContactRemoval(ctx context.Context, w http.ResponseWriter, orgID, memberID string) bool {
	member, err := s.store.AccountByID(ctx, memberID)
	if err != nil || !s.isBillingContact(ctx, orgID, member.Email) {
		return false
	}
	api.WriteProblem(w, api.ErrBillingContactMember())
	return true
}

// billingContactBlocksDeletion refuses deleting an account whose email an
// organization it belongs to bills to, for the same reason a member who is
// the billing contact cannot be removed (ADR-20261103131742).
func (s *server) billingContactBlocksDeletion(ctx context.Context, acct state.Account) *api.Problem {
	orgs, err := s.store.ListOrgsForAccount(ctx, acct.ID)
	if err != nil {
		return api.ErrCapacity("could not check organization billing contacts")
	}
	for _, org := range orgs {
		if org.Personal || org.DeletedPending || !s.isBillingContact(ctx, org.ID, acct.Email) {
			continue
		}
		problem := api.ErrBillingContactMember()
		problem.Detail = fmt.Sprintf("your email is organization %q's billing email; set another billing email with PUT /v1/orgs/%s/billing/email before deleting your account", org.Slug, org.Slug)
		return problem
	}
	return nil
}

// isBillingContact reports whether email is the organization's billing email
// while the organization has a billing provider customer. A billing identity
// that cannot be read counts as a customer, so the guards fail closed.
func (s *server) isBillingContact(ctx context.Context, orgID, email string) bool {
	org, err := state.BillingResourceAccount(ctx, s.store, orgID)
	if err != nil || !orgbilling.IsBillingContact(org.Email, email) {
		return false
	}
	billingAcct, err := s.accountForActiveBillingProvider(ctx, org)
	return err != nil || billingAcct.ProviderCustomerID != ""
}
