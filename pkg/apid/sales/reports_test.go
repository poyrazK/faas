package sales

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

type reportFake struct {
	*fakeStore
	report referrals.ManagerReport
	filter referrals.ReportFilter
	scope  referrals.Scope
	calls  int
	err    error
}

func (f *reportFake) ManagerReport(_ context.Context, scope referrals.Scope, filter referrals.ReportFilter) (referrals.ManagerReport, error) {
	f.calls++
	f.scope = scope
	f.filter = filter
	return f.report, f.err
}
func reportService() (*Service, *reportFake, state.Account) {
	s, base, acct := serviceFixture()
	s.Config.Members[acct.Email] = true
	f := &reportFake{fakeStore: base, report: referrals.ManagerReport{Reps: []referrals.ReportRep{}, Rows: []referrals.RepComparison{}, Collections: []referrals.FirstInvoiceCollections{}, ObservedAt: time.Now()}}
	s.Store = f
	return s, f, acct
}

func TestReportsRequireManagerVerificationAndMFA(t *testing.T) {
	for _, mutate := range []func(*Service, *state.Account){func(s *Service, _ *state.Account) { s.Config.Enabled = false }, func(s *Service, a *state.Account) { s.Config.Members[a.Email] = false }, func(s *Service, a *state.Account) { delete(s.Config.Members, a.Email) }, func(_ *Service, a *state.Account) { a.EmailVerifiedAt = nil }, func(_ *Service, a *state.Account) { a.MFAEnrolledAt = nil }} {
		s, f, acct := reportService()
		mutate(s, &acct)
		w := httptest.NewRecorder()
		s.Reports(w, httptest.NewRequest("GET", "/v1/sales/reports?tz=invalid-zone", nil), acct)
		if (w.Code != 403 && w.Code != 404) || f.calls != 0 {
			t.Fatal("unauthorized caller reached reporting", w.Code, f.calls, w.Body.String())
		}
	}
}

func TestReportWindowValidation(t *testing.T) {
	now := time.Date(2026, 11, 3, 22, 0, 0, 0, time.UTC)
	cases := []struct {
		query string
		valid bool
	}{{"from=2024-01-01&to=2024-12-31", true}, {"from=2024-01-01&to=2025-01-01", false}, {"from=2026-11-03&to=2026-11-02", false}, {"from=2026-02-30", false}, {"from=1999-12-31&to=1999-12-31", false}, {"from=2101-01-01&to=2101-01-01", false}, {"tz=Local", false}, {"tz=Not%2FAZone", false}, {"owner=not-an-id", false}, {"from=2026-11-01&to=2026-11-01&tz=America%2FNew_York", true}}
	for _, tc := range cases {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		_, ok := parseReportWindow(q, now)
		if ok != tc.valid {
			t.Fatal(tc, ok)
		}
	}
	w, ok := parseReportWindow(url.Values{"tz": {"Europe/Istanbul"}}, now)
	if !ok || w.From != "2026-11-01" || w.To != "2026-11-04" {
		t.Fatal("default dates did not follow reporting timezone", w, ok)
	}
}

func TestReportsPassValidatedFiltersAndHideStoreErrors(t *testing.T) {
	s, f, acct := reportService()
	w := httptest.NewRecorder()
	s.Reports(w, httptest.NewRequest("GET", "/v1/sales/reports?from=2026-11-01&to=2026-11-03&tz=Europe%2FIstanbul&owner="+acct.ID, nil), acct)
	if w.Code != 200 || f.calls != 1 || !f.scope.All || f.scope.OwnerID != acct.ID || f.filter.Owner != acct.ID || f.filter.From != "2026-11-01" || f.filter.To != "2026-11-03" || f.filter.Timezone != "Europe/Istanbul" {
		t.Fatal(w.Code, f.scope, f.filter, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"window"`) {
		t.Fatal(w.Header(), w.Body.String())
	}
	f.err = errors.New("private database detail")
	w = httptest.NewRecorder()
	s.Reports(w, httptest.NewRequest("GET", "/v1/sales/reports", nil), acct)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private database detail") {
		t.Fatal(w.Code, w.Body.String())
	}
	before := f.calls
	w = httptest.NewRecorder()
	s.Reports(w, httptest.NewRequest("GET", "/v1/sales/reports?from=invalid", nil), acct)
	if w.Code != 400 || f.calls != before {
		t.Fatal(w.Code, f.calls, w.Body.String())
	}
}

func TestReportsMarkupIsOnlyRenderedForManagers(t *testing.T) {
	s, _, acct := reportService()
	for _, manager := range []bool{true, false} {
		s.Config.Members[acct.Email] = manager
		w := httptest.NewRecorder()
		s.SalesPage(w, httptest.NewRequest("GET", "/dashboard/sales", nil), acct)
		if w.Code != 200 || strings.Contains(w.Body.String(), `id="reports"`) != manager || strings.Contains(w.Body.String(), "/reports.js") != manager {
			t.Fatal(manager, w.Code, w.Body.String())
		}
	}
	s.Config.Members[acct.Email] = true
	w := httptest.NewRecorder()
	s.CustomerPage(w, httptest.NewRequest("GET", "/dashboard/referrals", nil), acct)
	if strings.Contains(w.Body.String(), `id="reports"`) || strings.Contains(w.Body.String(), "/reports.js") {
		t.Fatal("customer page exposes manager reporting")
	}
}
