package apidserver

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid"
	"github.com/onebox-faas/faas/pkg/apid/sales"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

func (s *server) configureSales(getenv func(string) string) error {
	cfg, err := sales.FromEnv(getenv, "https://api."+s.platformHost())
	if err != nil {
		return fmt.Errorf("sales configuration: %w", err)
	}
	var store referrals.Store
	if cfg.Enabled {
		if s.platformHost() == "" || s.platformHost() == domainUnset {
			return fmt.Errorf("sales requires a configured platform domain")
		}
		provider, ok := s.store.(interface{ ReferralStore() referrals.Store })
		if !ok {
			return fmt.Errorf("sales requires the PostgreSQL referral store")
		}
		store = provider.ReferralStore()
	}
	s.WithSales(cfg, store)
	return nil
}

// WithSales is also the integration-test seam; production uses configureSales.
func (s *server) WithSales(cfg sales.Config, store referrals.Store) *server {
	s.sales = sales.Service{Config: cfg, Store: store, Account: s.store.AccountByID, Audit: s.audit.Emit, Key: func() []byte {
		if s.sessions == nil {
			return nil
		}
		return s.sessions.BindingKey()
	}}
	return s
}

func (s *server) salesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if !s.sales.Enabled() || !strings.EqualFold(apid.HostWithoutPort(r.Host), "sales."+s.platformHost()) {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/dashboard/sales", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /dashboard/referral-assets/{file}", s.sales.Asset)
	mux.Handle("GET /dashboard/sales", s.sessionAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acct, _ := AccountFrom(r.Context())
		s.sales.SalesPage(w, r, acct)
	})))
	mux.Handle("GET /dashboard/referrals", s.sessionAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acct, _ := AccountFrom(r.Context())
		s.sales.CustomerPage(w, r, acct)
	})))
	// This public form budget is separate from failed-auth limits.
	cfg := middleware.AuthLimitConfig{Window: time.Minute, MaxFailures: api.SalesIntakeRequestsPerMinute, CountStatuses: []int{middleware.CountEveryAttempt}}
	intakeLimit := middleware.AuthLimitWithLimiter(cfg, middleware.NewLimiter(cfg))
	mux.Handle("GET /v1/sales/inquiries/challenge", intakeLimit(http.HandlerFunc(s.sales.IntakeChallenge)))
	mux.Handle("POST /v1/sales/inquiries", intakeLimit(s.fromTrustedOrigin(http.HandlerFunc(s.sales.SubmitInquiry))))
	mux.HandleFunc("GET /v1/sales/inquiries", s.authLimited(s.requireSessionPrincipal(s.sales.Inquiries)))
	mux.HandleFunc("PUT /v1/sales/inquiries/{inquiry}/assignment", s.authLimited(s.requireSessionPrincipal(s.sales.AssignInquiry)))
	mux.HandleFunc("PUT /v1/sales/inquiries/{inquiry}/close", s.authLimited(s.requireSessionPrincipal(s.sales.CloseInquiry)))
	mux.HandleFunc("GET /v1/sales/reports", s.authLimited(s.requireSessionPrincipal(s.sales.Reports)))
	mux.HandleFunc("GET /v1/sales/team", s.authLimited(s.requireSessionPrincipal(s.sales.Team)))
	mux.HandleFunc("GET /v1/sales/leads", s.authLimited(s.requireSessionPrincipal(s.sales.Leads)))
	mux.HandleFunc("POST /v1/sales/leads", s.authLimited(s.requireSessionPrincipal(s.sales.SaveProspect)))
	mux.HandleFunc("PUT /v1/sales/leads/{lead}", s.authLimited(s.requireSessionPrincipal(s.sales.SaveProspect)))
	mux.HandleFunc("DELETE /v1/sales/leads/{lead}", s.authLimited(s.requireSessionPrincipal(s.sales.DeleteProspect)))
	mux.HandleFunc("GET /v1/sales/tasks", s.authLimited(s.requireSessionPrincipal(s.sales.Tasks)))
	mux.HandleFunc("POST /v1/sales/tasks", s.authLimited(s.requireSessionPrincipal(s.sales.SaveTask)))
	mux.HandleFunc("PUT /v1/sales/tasks/{task}", s.authLimited(s.requireSessionPrincipal(s.sales.SaveTask)))
	mux.HandleFunc("GET /v1/sales/me", s.authLimited(s.requireSessionPrincipal(s.sales.SalesData)))
	mux.HandleFunc("POST /v1/sales/codes", s.authLimited(s.requireSessionPrincipal(s.sales.SalesLink)))
	mux.HandleFunc("PATCH /v1/sales/codes/{code}", s.authLimited(s.requireSessionPrincipal(s.sales.SetLinkActive)))
	mux.HandleFunc("PATCH /v1/sales/customers/{account}", s.authLimited(s.requireSessionPrincipal(s.sales.UpdateLead)))
	mux.HandleFunc("GET /v1/referrals/me", s.authLimited(s.requireSessionPrincipal(s.sales.CustomerData)))
	mux.HandleFunc("POST /v1/referrals/codes", s.authLimited(s.requireSessionPrincipal(s.sales.CustomerLink)))
	mux.HandleFunc("PATCH /v1/referrals/codes/{code}", s.authLimited(s.requireSessionPrincipal(s.sales.SetLinkActive)))
}

func (s *server) isSalesMember(acct state.Account) bool {
	if !s.sales.Enabled() {
		return false
	}
	_, member := s.sales.Config.Member(acct.Email)
	return member
}
