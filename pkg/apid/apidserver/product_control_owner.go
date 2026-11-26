package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) registerProductOwnerConsole(mux *http.ServeMux) {
	p := &s.productControl
	browser := func(next accountHandler) http.HandlerFunc {
		h := s.dashboardChain(s.sessionAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if acct, ok := AccountFrom(r.Context()); ok {
				next(w, r, acct)
			}
		})))
		return p.Gate(h.ServeHTTP)
	}
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control", browser(s.requireMFA(p.OwnerConsole)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/outstanding-balances", browser(s.requireMFA(p.OwnerOutstandingBalances)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/customers", browser(s.requireMFA(p.OwnerCustomersOverview)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/activity", browser(s.requireMFA(p.OwnerCustomerActivity)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/help", browser(s.requireMFA(p.OwnerHelp)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/email", browser(s.requireMFA(p.OwnerEmail)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/email-recoveries", browser(s.requireMFA(p.OwnerEmailRecoveries)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/email-opt-outs", browser(s.requireMFA(p.OwnerEmailOptOuts)))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/email/{delivery}/retry", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerEmailRetry))))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/help/{request}", browser(s.requireMFA(p.OwnerHelpRequest)))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/help/{request}", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerHelpUpdate))))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/doctor", browser(s.requireMFA(p.OwnerDoctor)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/plan-policy", browser(s.requireMFA(p.OwnerPlanPolicy)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/bulk-plans", browser(s.requireMFA(p.OwnerBulkPlans)))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/bulk-plans/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerBulkPlansReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/bulk-plans/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerBulkPlansConfirm))))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/team-policy", browser(s.requireMFA(p.OwnerTeamPolicy)))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/team-policy/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerTeamPolicyReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/team-policy/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerTeamPolicyConfirm))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/plan-policy/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerPlanPolicyReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/plan-policy/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerPlanPolicyConfirm))))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/access", browser(s.requireMFA(p.OwnerAccess)))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/access/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerAccessReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/access/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerAccessConfirm))))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/setup", browser(s.requireMFA(p.OwnerSetup)))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/onboarding", browser(s.requireMFA(p.OwnerOnboarding)))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/onboarding/session", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerOnboardingSession))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/onboarding/disconnect", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerOnboardingDisconnect))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/onboarding/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerOnboardingReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/onboarding/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerOnboardingConfirm))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/setup/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerSetupReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/setup/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerSetupConfirm))))
	mux.HandleFunc("GET /dashboard/apps/{slug}/product-control/runs/{operation}", browser(s.requireMFA(p.OwnerRun)))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/billing-batch/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerBillingBatchReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/billing-batch/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerBillingBatchConfirm))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/invoice-batch/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerInvoiceBatchReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/invoice-batch/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerInvoiceBatchConfirm))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/payments/refresh-selected", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerRefreshSelectedBalances))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/payments/refresh", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerRefreshPayment))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/payments/check-stripe-send", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerCheckStripeSend))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/payments/stripe-send-batch/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerStripeSendBatchReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/payments/stripe-send-batch/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerStripeSendBatchConfirm))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/review", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerReview))))
	mux.HandleFunc("POST /dashboard/apps/{slug}/product-control/confirm", browser(s.requireMFA(s.requireVerifiedEmail(p.OwnerConfirm))))
}

func (s *server) renderProductOwner(w http.ResponseWriter, r *http.Request, acct state.Account, data dashboard.ProductControlData) {
	page := dashboard.Page{Title: data.AppSlug + " Product Control", Body: "product_control", Account: dashboardAccountView(acct, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		s.log.ErrorContext(r.Context(), "render Product Control console", "error", err)
		http.Error(w, "Product Control is temporarily unavailable", http.StatusInternalServerError)
	}
}

func (s *server) productControlConsoleURL(slug string) string {
	if !s.productControl.Enabled {
		return ""
	}
	return "/dashboard/apps/" + slug + "/product-control"
}
