package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/apid/supportportal"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const supportPortalAction = "customer_support_portal"
const supportPortalCookie = "faas_csrf_support_portal"

func (s *server) registerSupportPortalRoutes(mux *http.ServeMux) {
	for _, path := range []string{"/dashboard/support", "/dashboard/support/{ticket}", "/dashboard/orgs/{org}/support", "/dashboard/orgs/{org}/support/{ticket}"} {
		for _, method := range []string{"GET", "POST"} {
			mux.Handle(method+" "+path, s.dashboardChain(s.sessionAuth(s.supportPortal(false))))
		}
	}
	for _, path := range []string{"/dashboard/admin/support", "/dashboard/admin/support/{ticket}"} {
		mux.Handle("GET "+path, s.dashboardChain(s.sessionAuth(s.supportPortal(true))))
		mux.Handle("POST "+path, s.dashboardChain(s.sessionAuth(s.supportPortal(true))))
	}
	s.registerSupportAttentionRoutes(mux)
}

func (s *server) supportPortal(staff bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct, ok := AccountFrom(r.Context())
		if !ok {
			writeDashboardUnauthorized(w, r)
			return
		}
		portal := supportportal.Handler{
			Service: s.customerSupport,
			Token:   func(w http.ResponseWriter, _ *http.Request) (string, error) { return s.supportPortalToken(w, acct.ID) },
			Verify: func(r *http.Request) error {
				return middleware.VerifyAuthenticatedNamed(s.sessions, r, supportPortalAction, acct.ID, supportPortalCookie)
			},
			Submit: func(w http.ResponseWriter, r *http.Request, endpoint string, staff bool) {
				s.supportPortalSubmit(w, r, acct, endpoint, staff)
			},
			Render: func(w http.ResponseWriter, r *http.Request, data supportportal.Data) {
				title := "Customer support"
				if staff {
					title = "Support inbox"
				}
				s.renderCustomDashboardPage(w, r, acct, title, "support", data)
			},
			Customer: s.supportCustomerContext,
		}
		portal.Serve(w, r, support.Actor{ID: acct.ID, Session: true}, staff)
	}
}

// supportCustomerContext loads the account panel for a staff ticket
// (ADR-20261112170000): the customer's account, plan and status and the
// tenant 360 diagnosis. Portal has already checked the staff roster and the
// MFA-completed session. Any failure yields an Unavailable panel; the
// ticket page itself still renders.
func (s *server) supportCustomerContext(r *http.Request, ticket *support.StaffTicket) *supportportal.CustomerContext {
	unavailable := &supportportal.CustomerContext{Unavailable: true}
	accountID, orgID, err := s.customerSupport.TicketOwner(r.Context(), ticket.Ticket.ID)
	if err != nil || accountID == "" {
		return unavailable
	}
	target, err := s.store.AccountByID(r.Context(), accountID)
	if err != nil {
		return unavailable
	}
	panel := &supportportal.CustomerContext{AccountID: target.ID, Plan: string(target.Plan), Status: string(target.Status)}
	if orgID != "" {
		if org, err := s.store.OrgByID(r.Context(), orgID); err == nil {
			panel.OrgSlug = org.Slug
		}
	}
	if apps, err := s.store.ListApps(r.Context(), target.ID); err == nil {
		panel.Apps = len(apps)
	}
	month := time.Now().UTC()
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	billing, err := projectObsTenantBilling(r, s.store, s.billingProvider, target, month)
	if err != nil {
		billing = api.ObsTenantBilling{}
	}
	panel.Diagnosis = s.buildTenantDiagnosis(r.Context(), target, billing)
	s.loadTicketSnapshot(r.Context(), ticket.Ticket.ID, panel)
	return panel
}

func (s *server) supportPortalToken(w http.ResponseWriter, account string) (string, error) {
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, supportPortalAction, account, supportPortalCookie)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{Name: supportPortalCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	return token, nil
}

func (s *server) supportPortalSubmit(w http.ResponseWriter, r *http.Request, acct state.Account, endpoint string, staff bool) {
	handler := s.customerSupportHandler(endpoint, staff)
	if staff && endpoint != "read" {
		handler = s.requireStepUpStrict(5 * time.Minute)(handler)
	}
	s.requireSameOrigin(s.requireSessionPrincipal(handler))(w, r, acct)
}
