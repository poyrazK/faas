package apidserver

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) WithProductControlEnabled(enabled bool) *server {
	s.productControl.Enabled = enabled
	s.operation.ProductControlEnabled = enabled
	return s
}

func (s *server) configureProductControl(cfg Config, getenv func(string) string) {
	enabled := cfg.ProductControlEnabled
	if value := getenv("FAAS_PRODUCT_CONTROL_ENABLED"); value != "" {
		enabled = value == "1"
	}
	s.WithProductControlEnabled(enabled)
}

func (s *server) registerProductControl(mux *http.ServeMux) {
	p := &s.productControl
	read := func(next accountHandler) http.HandlerFunc {
		return p.Gate(s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(next))))
	}
	write := func(next accountHandler) http.HandlerFunc {
		return p.Gate(s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(next)))))
	}
	mux.HandleFunc("GET /v1/apps/{slug}/product", read(p.GetCatalog))
	mux.HandleFunc("GET /v1/apps/{slug}/product/doctor", read(p.Doctor))
	mux.HandleFunc("GET /v1/apps/{slug}/product/plan-policy", read(p.GetPlanPolicy))
	mux.HandleFunc("GET /v1/apps/{slug}/product/team-policy", read(p.GetTeamPolicy))
	mux.HandleFunc("POST /v1/apps/{slug}/product/team-policy/preview", write(p.PreviewTeamPolicy))
	mux.HandleFunc("PUT /v1/apps/{slug}/product/team-policy", write(p.ApplyTeamPolicy))
	mux.HandleFunc("POST /v1/apps/{slug}/product/plan-policy/preview", write(p.PreviewPlanPolicy))
	mux.HandleFunc("PUT /v1/apps/{slug}/product/plan-policy", write(p.ApplyPlanPolicy))
	mux.HandleFunc("GET /v1/apps/{slug}/product/access", read(p.GetAccess))
	mux.HandleFunc("POST /v1/apps/{slug}/product/access/preview", write(p.PreviewAccess))
	mux.HandleFunc("PUT /v1/apps/{slug}/product/access", write(p.ApplyAccess))
	mux.HandleFunc("PUT /v1/apps/{slug}/product", write(p.PutCatalog))
	mux.HandleFunc("GET /v1/apps/{slug}/product/customers", read(p.ListCustomers))
	mux.HandleFunc("GET /v1/apps/{slug}/product/customers/overview", read(p.CustomersOverview))
	mux.HandleFunc("GET /v1/apps/{slug}/product/customers/activity", read(p.CustomerActivity))
	mux.HandleFunc("GET /v1/apps/{slug}/product/help", read(p.HelpList))
	mux.HandleFunc("GET /v1/apps/{slug}/product/email-deliveries", read(p.EmailDeliveries))
	mux.HandleFunc("GET /v1/apps/{slug}/product/email-recoveries", read(p.EmailRecoveries))
	mux.HandleFunc("GET /v1/apps/{slug}/product/email-opt-outs", read(p.EmailOptOuts))
	mux.HandleFunc("POST /v1/apps/{slug}/product/email-deliveries/{delivery}/retry", write(p.EmailRetry))
	mux.HandleFunc("GET /v1/apps/{slug}/product/help/{request}", read(p.HelpGet))
	mux.HandleFunc("PUT /v1/apps/{slug}/product/help/{request}", write(p.HelpUpdate))
	mux.HandleFunc("PUT /v1/apps/{slug}/product/customers/{tenant}", write(p.SetCustomer))
	mux.HandleFunc("PUT /v1/apps/{slug}/product/customers/{tenant}/trial", write(p.PutCustomerTrial))
	mux.HandleFunc("DELETE /v1/apps/{slug}/product/customers/{tenant}/trial", write(p.DeleteCustomerTrial))
	mux.HandleFunc("POST /v1/apps/{slug}/product/onboarding/workspaces", write(p.OnboardingWorkspaces))
	mux.HandleFunc("POST /v1/apps/{slug}/product/onboarding/preview", write(p.OnboardingPreview))
	mux.HandleFunc("POST /v1/apps/{slug}/product/onboarding/confirm", write(p.OnboardingConfirm))
	mux.HandleFunc("GET /v1/apps/{slug}/product/onboarding/{onboarding}", read(p.OnboardingStatus))
	mux.HandleFunc("GET /v1/apps/{slug}/product/usage", read(p.Usage))
	mux.HandleFunc("GET /v1/apps/{slug}/product/operations/{operation}/explain", read(p.Explain))
	mux.HandleFunc("GET /v1/apps/{slug}/product/billing/review", read(p.BillingReview))
	mux.HandleFunc("GET /v1/apps/{slug}/product/billing/receipts", read(p.BillingReceipts))
	mux.HandleFunc("GET /v1/apps/{slug}/product/billing/documents", read(p.BillingDocuments))
	mux.HandleFunc("POST /v1/apps/{slug}/product/billing/statements/finalize", write(p.BillingFinalize))
	mux.HandleFunc("POST /v1/apps/{slug}/product/billing/receipts/{operation}/waive", write(p.BillingWaive))
	mux.HandleFunc("POST /v1/apps/{slug}/product/billing/credits", write(p.BillingCredit))
	mux.HandleFunc("POST /v1/apps/{slug}/product/billing/documents/{document}/handoff", write(p.BillingHandoff))
	mux.HandleFunc("GET /v1/apps/{slug}/product/billing/documents/{document}/handoff", read(p.BillingGetHandoff))
	mux.HandleFunc("POST /v1/apps/{slug}/product/billing/documents/{document}/invoice", write(p.BillingExport))
	mux.HandleFunc("GET /v1/apps/{slug}/product/billing/documents/{document}/invoice", read(p.BillingGetExport))
	mux.HandleFunc("GET /v1/apps/{slug}/product/billing/overdue-policy", read(p.GetOverduePolicy))
	mux.HandleFunc("PUT /v1/apps/{slug}/product/billing/overdue-policy", write(p.PutOverduePolicy))
	mux.HandleFunc("DELETE /v1/apps/{slug}/product/billing/overdue-policy", write(p.DeleteOverduePolicy))
	mux.HandleFunc("GET /v1/apps/{slug}/product/billing/overdue-holds", read(p.OverdueHolds))
	mux.HandleFunc("GET /v1/platform-tenant-self/product/billing", p.Gate(s.authLimited(s.requireScope(api.ScopePlatformTenantOperationsRead)(p.CustomerBilling))))
	mux.HandleFunc("GET /v1/platform-tenant-self/product", p.Gate(s.authLimited(s.requireScope(api.ScopePlatformTenantOperationsRead)(p.Portal))))
	mux.HandleFunc("POST /v1/platform-tenant-self/product/start", p.Gate(s.authLimited(s.requireScope(api.ScopePlatformTenantOperationsManage)(p.Start))))
	mux.HandleFunc("POST /v1/platform-tenant-self/product/submissions/lookup", p.Gate(s.authLimited(s.requireScope(api.ScopePlatformTenantOperationsRead)(p.LookupSubmission))))
	mux.HandleFunc("GET /v1/product-control/portal", p.Gate(p.PortalPage))
	mux.HandleFunc("POST /v1/product-control/email/resend-events", p.Gate(p.EmailFeedback))
	mux.HandleFunc("GET /v1/product-control/portal-plans.js", p.Gate(p.PortalPlans))
	mux.HandleFunc("GET /v1/product-control/portal-team.js", p.Gate(p.PortalTeam))
	mux.HandleFunc("GET /v1/product-control/portal-guidance.js", p.Gate(p.PortalGuidance))
	mux.HandleFunc("GET /v1/product-control/portal-help.js", p.Gate(p.PortalHelp))
	mux.HandleFunc("GET /v1/product-control/portal-email.js", p.Gate(p.PortalEmail))
	mux.HandleFunc("GET /v1/product-control/portal-notifications.js", p.Gate(p.PortalNotifications))
	mux.HandleFunc("GET /v1/product-control/portal-activity.js", p.Gate(p.PortalActivity))
	mux.HandleFunc("GET /v1/product-control/portal-run.js", p.Gate(p.PortalRun))
	mux.HandleFunc("GET /v1/product-control/portal-history.js", p.Gate(p.PortalHistory))
	mux.HandleFunc("GET /v1/product-control/portal-signup.js", p.Gate(p.PortalSignup))
	mux.HandleFunc("GET /v1/product-control/portal.js", p.Gate(p.PortalScript))
	mux.HandleFunc("GET /v1/product-control/portal-core.js", p.Gate(p.PortalCore))
	mux.HandleFunc("GET /v1/product-control/portal.css", p.Gate(p.PortalStyle))
	s.registerProductCustomers(mux)
	s.registerProductOwnerConsole(mux)
}

func (s *server) registerProductCustomers(mux *http.ServeMux) {
	p := &s.productControl
	limited := func(next http.HandlerFunc) http.HandlerFunc {
		return p.Gate(middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{Log: s.log}, s.apiAuthLimiter)(next).ServeHTTP)
	}
	mux.HandleFunc("GET /v1/product-control/email/unsubscribe", limited(p.EmailUnsubscribePage))
	mux.HandleFunc("POST /v1/product-control/email/unsubscribe", limited(p.EmailUnsubscribe))
	mux.HandleFunc("GET /v1/product-control/email-unsubscribe.js", p.Gate(p.EmailUnsubscribeScript))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/session", limited(p.CustomerLogin))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/account-recovery", limited(p.CustomerRecovery))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/session/refresh", limited(p.CustomerRefresh))
	mux.HandleFunc("DELETE /v1/product-control/customer/{slug}/session", limited(p.CustomerLogout))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/workspaces", limited(p.CustomerWorkspaces))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/offer", limited(p.CustomerSignupOffer))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/signup", limited(p.CustomerSignup))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/product", limited(p.Customer("", p.CustomerPortal)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/guidance", limited(p.Customer("", p.CustomerGuidance)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/email-preferences", limited(p.Customer("", p.CustomerEmailPreferences)))
	mux.HandleFunc("PUT /v1/product-control/customer/{slug}/email-preferences", limited(p.Customer("", p.CustomerEmailUpdate)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/email-recovery", limited(p.Customer("", p.CustomerEmailRecover)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/email-recoveries", limited(p.Customer("", p.CustomerEmailRecoveries)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/email-opt-outs", limited(p.Customer("", p.CustomerEmailOptOuts)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/email-deliveries", limited(p.Customer("", p.CustomerEmailDeliveries)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/notifications", limited(p.Customer("", p.CustomerNotifications)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/activity", limited(p.Customer("", p.CustomerWorkspaceActivity)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/notifications/read", limited(p.Customer("", p.CustomerNotificationRead)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/help", limited(p.Customer("", p.CustomerHelpCreate)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/help", limited(p.Customer("", p.CustomerHelpList)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/help/{request}", limited(p.Customer("", p.CustomerHelpGet)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/payment-method", limited(p.Customer(api.ProductPermissionManageBilling, p.CustomerPaymentMethod)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/payment-method/checkout", limited(p.Customer(api.ProductPermissionManageBilling, p.CustomerPaymentMethodCheckout)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/payment-method/complete", limited(p.Customer(api.ProductPermissionManageBilling, p.CustomerPaymentMethodComplete)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/billing-details", limited(p.Customer(api.ProductPermissionManageBilling, p.CustomerBillingDetails)))
	mux.HandleFunc("PUT /v1/product-control/customer/{slug}/billing-details", limited(p.Customer(api.ProductPermissionManageBilling, p.PutCustomerBillingDetails)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/plans", limited(p.Customer(api.ProductPermissionManagePlan, p.CustomerPlans)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/team", limited(p.Customer(api.ProductPermissionManageTeam, p.CustomerTeam)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/team/preview", limited(p.Customer(api.ProductPermissionManageTeam, p.CustomerTeamPreview)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/team/confirm", limited(p.Customer(api.ProductPermissionManageTeam, p.CustomerTeamConfirm)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/team/actions", limited(p.Customer(api.ProductPermissionManageTeam, p.CustomerTeamHistory)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/team/actions/{change}", limited(p.Customer(api.ProductPermissionManageTeam, p.CustomerTeamAction)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/plans/preview", limited(p.Customer(api.ProductPermissionManagePlan, p.CustomerPlanPreview)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/plans/confirm", limited(p.Customer(api.ProductPermissionManagePlan, p.CustomerPlanConfirm)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/plans/changes", limited(p.Customer(api.ProductPermissionManagePlan, p.CustomerPlanHistory)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/plans/changes/{change}", limited(p.Customer(api.ProductPermissionManagePlan, p.CustomerPlanChange)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/billing", limited(p.Customer(api.ProductPermissionViewBilling, p.CustomerBilling)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/start", limited(p.Customer(api.ProductPermissionRunWork, p.CustomerStart)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/submissions/lookup", limited(p.Customer(api.ProductPermissionViewWork, p.LookupSubmission)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/work-history", limited(p.Customer(api.ProductPermissionViewWork, p.CustomerWorkHistory)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/operations/{id}", limited(p.Customer(api.ProductPermissionViewWork, s.operation.GetPlatformTenantSelfOperation)))
	// The independent receipt outlives operation detail retention. Its store
	// boundary validates account/app/tenant and current customer authority.
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/operations/{operation}/details", limited(p.Customer(api.ProductPermissionViewWork, p.CustomerRunDetails)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/operations/{id}/events", limited(p.Customer(api.ProductPermissionViewWork, s.operation.GetPlatformTenantSelfOperationEvents)))
	mux.HandleFunc("POST /v1/product-control/customer/{slug}/operations/{id}/cancel", limited(p.Customer(api.ProductPermissionRunWork, s.operation.CancelPlatformTenantSelfOperation)))
	mux.HandleFunc("GET /v1/product-control/customer/{slug}/operations/{id}/artifacts/{artifact}", limited(p.Customer(api.ProductPermissionViewWork, s.operation.DownloadPlatformTenantOperationArtifact)))
}

func (s *server) productAuthOrigin(ctx context.Context, acct state.Account, slug string) (state.App, string, error) {
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		return state.App{}, "", state.ErrNotFound
	}
	isAuth, err := s.appHasAuthBinding(ctx, acct, app)
	if err != nil {
		return state.App{}, "", err
	}
	if !isAuth {
		return state.App{}, "", state.ErrProductAccess
	}
	// Use the platform app hostname, not a caller-supplied URL or a custom
	// domain. This cannot turn owner configuration into an arbitrary proxy.
	origin := appURLForDomain(app.Slug, s.domain)
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return state.App{}, "", state.ErrProductAccess
	}
	return app, origin, nil
}
