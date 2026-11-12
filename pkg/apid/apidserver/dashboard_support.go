package apidserver

import (
	"net/http"
	"time"

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
		}
		portal.Serve(w, r, support.Actor{ID: acct.ID, Session: true}, staff)
	}
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
