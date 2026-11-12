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
	s.customerSupport = &support.Service{Pool: pool, Webhook: hook, Operator: s.supportBootstrapManager}
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
		s.customerSupport.Serve(w, r, a, endpoint, staff)
	}
}
func (s *server) registerCustomerSupportRoutes(mux *http.ServeMux) {
	customer := func(endpoint string) http.HandlerFunc {
		return s.authLimited(s.requireScope(api.ScopeAdmin)(s.customerSupportHandler(endpoint, false)))
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
	s.registerSupportStaffRoutes(mux)
}
func (s *server) registerSupportStaffRoutes(mux *http.ServeMux) {
	for _, route := range []struct{ method, path, endpoint string }{
		{"GET", "/tickets", "list"}, {"GET", "/tickets/{ticket}", "get"}, {"GET", "/tickets/{ticket}/messages", "messages"},
		{"POST", "/tickets/{ticket}/messages", "reply"}, {"POST", "/tickets/{ticket}/notes", "note"}, {"GET", "/tickets/{ticket}/events", "events"},
		{"POST", "/tickets/{ticket}/close", "close"}, {"POST", "/tickets/{ticket}/reopen", "reopen"}, {"POST", "/tickets/{ticket}/pause", "pause"}, {"POST", "/tickets/{ticket}/resume", "resume"}, {"POST", "/tickets/{ticket}/triage", "triage"},
		{"POST", "/availability", "availability"}, {"GET", "/policies", "policies"}, {"POST", "/policies", "policy_save"}, {"GET", "/teams", "teams"}, {"POST", "/teams", "team_save"}, {"GET", "/staff", "staff"}, {"POST", "/staff", "staff_save"},
		{"GET", "/events", "config_events"}, {"GET", "/report", "report"},
		{"GET", "/tickets/{ticket}/engineering", "engineering"}, {"POST", "/tickets/{ticket}/engineering", "engineering_save"},
		{"POST", "/tickets/{ticket}/engineering/preview", "engineering_preview"}, {"POST", "/tickets/{ticket}/engineering/publish", "engineering_publish"},
		{"POST", "/tickets/{ticket}/redactions/preview", "redaction_preview"}, {"POST", "/tickets/{ticket}/redactions/apply", "redaction_apply"},
	} {
		h := s.customerSupportHandler(route.endpoint, true)
		if route.method == "POST" {
			h = s.requireSameOrigin(s.requireStepUpStrict(5 * time.Minute)(h))
		}
		mux.HandleFunc(route.method+" /v1/admin/support"+route.path, s.authLimited(s.requireSessionPrincipal(h)))
	}
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
