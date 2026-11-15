package apidserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) configureCustomerSupport(ctx context.Context, pool *pgxpool.Pool, getenv func(string) string) error {
	hook, e := support.NewWebhook(getenv("FAAS_SUPPORT_WEBHOOK_URL"), getenv("FAAS_SUPPORT_WEBHOOK_SECRET"))
	if e != nil {
		return e
	}
	s.customerSupport = &support.Service{Pool: pool, Webhook: hook, Operator: s.supportBootstrapManager,
		Notify: s.sendSupportNotification, BootstrapEmails: s.supportNotificationBootstrapEmails, EmailAllowed: s.supportNotificationEmailAllowed}
	if pool != nil {
		go s.customerSupport.Run(ctx)
	}
	return nil
}
func (s *server) customerSupportHandler(endpoint string, staff bool) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		actor, key, ok := middleware.AccountFromContext(r)
		if !ok {
			api.WriteProblem(w, api.NewProblem(401, api.CodeUnauthorized, "Unauthorized", "Sign in is required."))
			return
		}
		a := support.Actor{ID: actor.ID, Session: key == nil}
		if key != nil {
			if key.AppID != "" || key.PlatformTenantID != "" || key.HostingSiteID != "" || key.HostingOIDCTrustID != "" || key.ProjectLimited {
				api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Support principal rejected", "Support requires an unrestricted account or organization admin principal."))
				return
			}
			a.KeyOrgID = key.OrgID
		}
		if endpoint == "create" && !staff {
			// ADR-20261112210000: snapshot account health with the new ticket.
			s.serveSupportCreate(w, r, func(w http.ResponseWriter) { s.customerSupport.Serve(w, r, a, endpoint, staff) })
			return
		}
		s.customerSupport.Serve(w, r, a, endpoint, staff)
	}
}
func (s *server) registerCustomerSupportRoutes(mux *http.ServeMux) {
	customer := func(endpoint string) http.HandlerFunc {
		h := s.customerSupportHandler(endpoint, false)
		if endpoint == "read" {
			h = s.requireSameOrigin(h)
		}
		return s.authLimited(s.requireScope(api.ScopeAdmin)(h))
	}
	mux.HandleFunc("GET /v1/support/tickets", customer("list"))
	mux.HandleFunc("POST /v1/support/tickets", customer("create"))
	mux.HandleFunc("GET /v1/support/tickets/{ticket}", customer("get"))
	mux.HandleFunc("GET /v1/support/tickets/{ticket}/messages", customer("messages"))
	mux.HandleFunc("POST /v1/support/tickets/{ticket}/messages", customer("reply"))
	mux.HandleFunc("POST /v1/support/tickets/{ticket}/close", customer("close"))
	mux.HandleFunc("POST /v1/support/tickets/{ticket}/reopen", customer("reopen"))
	mux.HandleFunc("GET /v1/support/tickets/{ticket}/satisfaction", customer("satisfaction"))
	mux.HandleFunc("POST /v1/support/tickets/{ticket}/satisfaction", customer("satisfaction"))
	mux.HandleFunc("GET /v1/orgs/{org}/support/tickets", customer("list"))
	mux.HandleFunc("POST /v1/orgs/{org}/support/tickets", customer("create"))
	mux.HandleFunc("GET /v1/orgs/{org}/support/tickets/{ticket}", customer("get"))
	mux.HandleFunc("GET /v1/orgs/{org}/support/tickets/{ticket}/messages", customer("messages"))
	mux.HandleFunc("POST /v1/orgs/{org}/support/tickets/{ticket}/messages", customer("reply"))
	mux.HandleFunc("POST /v1/orgs/{org}/support/tickets/{ticket}/close", customer("close"))
	mux.HandleFunc("POST /v1/orgs/{org}/support/tickets/{ticket}/reopen", customer("reopen"))
	mux.HandleFunc("GET /v1/orgs/{org}/support/tickets/{ticket}/satisfaction", customer("satisfaction"))
	mux.HandleFunc("POST /v1/orgs/{org}/support/tickets/{ticket}/satisfaction", customer("satisfaction"))
	s.registerSupportNotificationRoutes(mux, customer)
	s.registerSupportStaffRoutes(mux)
	s.registerSupportPortalRoutes(mux)
}

// registerSupportStaffRoutes spells every route out so route tables, the
// read-policy test and the operator console's contract check can read them.
// Reads need an MFA-completed staff session on the support roster (checked by
// the staff-mode handler); writes also need fresh step-up and the trusted
// origin.
func (s *server) registerSupportStaffRoutes(mux *http.ServeMux) {
	write := func(endpoint string) http.HandlerFunc {
		h := s.requireSameOrigin(s.requireStepUpStrict(5 * time.Minute)(s.customerSupportHandler(endpoint, true)))
		return s.authLimited(s.requireSessionPrincipal(h))
	}
	mux.HandleFunc("GET /v1/admin/support/tickets", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("list", true))))
	mux.HandleFunc("GET /v1/admin/support/tickets/{ticket}", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("get", true))))
	mux.HandleFunc("GET /v1/admin/support/tickets/{ticket}/messages", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("messages", true))))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/messages", write("reply"))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/notes", write("note"))
	mux.HandleFunc("GET /v1/admin/support/tickets/{ticket}/events", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("events", true))))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/close", write("close"))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/reopen", write("reopen"))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/pause", write("pause"))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/resume", write("resume"))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/triage", write("triage"))
	mux.HandleFunc("POST /v1/admin/support/availability", write("availability"))
	mux.HandleFunc("GET /v1/admin/support/policies", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("policies", true))))
	mux.HandleFunc("POST /v1/admin/support/policies", write("policy_save"))
	mux.HandleFunc("GET /v1/admin/support/teams", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("teams", true))))
	mux.HandleFunc("POST /v1/admin/support/teams", write("team_save"))
	mux.HandleFunc("GET /v1/admin/support/staff", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("staff", true))))
	mux.HandleFunc("POST /v1/admin/support/staff", write("staff_save"))
	mux.HandleFunc("GET /v1/admin/support/events", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("config_events", true))))
	mux.HandleFunc("GET /v1/admin/support/report", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("report", true))))
	mux.HandleFunc("GET /v1/admin/support/tickets/{ticket}/engineering", s.authLimited(s.requireSessionPrincipal(s.customerSupportHandler("engineering", true))))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/engineering", write("engineering_save"))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/engineering/preview", write("engineering_preview"))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/engineering/publish", write("engineering_publish"))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/redactions/preview", write("redaction_preview"))
	mux.HandleFunc("POST /v1/admin/support/tickets/{ticket}/redactions/apply", write("redaction_apply"))
	// ADR-20261115183907: the ticket's account panel for the operator console.
	mux.HandleFunc("GET /v1/admin/support/tickets/{ticket}/customer", s.authLimited(s.requireSessionPrincipal(s.supportTicketCustomer)))
}

// supportBootstrapManager receives the current database email only after the
// support transaction verifies active status, email verification and MFA.
func (s *server) supportBootstrapManager(_ string, email string) bool {
	if s.adminAllowlist == nil {
		return false
	}
	_, allowed := s.adminAllowlist.emails[strings.ToLower(email)]
	return allowed
}
