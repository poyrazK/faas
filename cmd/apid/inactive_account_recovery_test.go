package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestInactiveAccount_CanStillPay — a suspended account got 402 from every
// /v1 route, including GET /v1/billing/portal: the command the suspension
// email tells the customer to run. A Free account stopped at its quota
// could not upgrade, nobody could export their data, and a dunning deletion
// ("pay to keep your account") could not be paid from the product.
func TestInactiveAccount_CanStillPay(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, s *state.MemStore, id string)
	}{
		{"suspended", func(t *testing.T, s *state.MemStore, id string) {
			if err := s.UpdateAccountStatus(t.Context(), id, state.AccountSuspended); err != nil {
				t.Fatal(err)
			}
		}},
		{"dunning deleted_pending", func(t *testing.T, s *state.MemStore, id string) {
			if err := s.MarkDunningStep(t.Context(), id, state.AccountActive, state.AccountPastDue); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDunningStep(t.Context(), id, state.AccountPastDue, state.AccountSuspended); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDunningStep(t.Context(), id, state.AccountSuspended, state.AccountDeletedPending); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := setupChangePlan(t, api.PlanHobby, "si_test")
			tc.setup(t, env.store, env.acct.ID)
			for _, rt := range []struct {
				method, path string
				want         int
			}{
				{"GET", "/v1/billing/portal", http.StatusOK},
				{"GET", "/v1/billing/status", http.StatusOK},
				{"GET", "/v1/account", http.StatusOK},
				{"GET", "/v1/account/export", http.StatusOK},
				{"GET", "/v1/usage", http.StatusOK},
				// Still closed: workload and configuration routes.
				{"GET", "/v1/apps", http.StatusPaymentRequired},
				{"POST", "/v1/apps", http.StatusPaymentRequired},
				{"POST", "/v1/billing/cancel", http.StatusPaymentRequired},
			} {
				rec := httptest.NewRecorder()
				r := httptest.NewRequest(rt.method, rt.path, nil)
				r.Header.Set("Authorization", "Bearer "+env.key)
				env.h.ServeHTTP(rec, r)
				if rec.Code != rt.want {
					t.Errorf("%s %s = %d, want %d\nbody = %s", rt.method, rt.path, rec.Code, rt.want, rec.Body.String())
				}
			}
		})
	}
}

// TestInactiveAccount_DashboardLandsOnRecoveryPage — sessionAuth sent every
// suspended session to /login, which signed the customer in and bounced
// them straight back: the 402's "resolve billing to continue:
// /dashboard/billing" link was a redirect loop.
func TestInactiveAccount_DashboardLandsOnRecoveryPage(t *testing.T) {
	srv, _, store, mgr := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStatus(t.Context(), acct.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	raw, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	sid := &http.Cookie{Name: sessionCookie, Value: raw}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(sid)
		srv.ServeHTTP(rec, r)
		return rec
	}
	for _, path := range []string{"/dashboard/billing", "/dashboard/account", "/dashboard/usage"} {
		if rec := get(path); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d (Location %q), want 200", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	for _, path := range []string{"/dashboard/", "/dashboard/apps"} {
		rec := get(path)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/dashboard/billing" {
			t.Errorf("GET %s = %d Location=%q, want 302 /dashboard/billing", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// TestRestore_DunningDeletionSaysPay — the store refuses a self-service
// restore of a deletion dunning scheduled, and the handler reported it as
// "the 30-day grace window has lapsed" on the day the deletion was
// scheduled. The answer is 402 with the way out: pay.
func TestRestore_DunningDeletionSaysPay(t *testing.T) {
	env, _ := setupChangePlan(t, api.PlanHobby, "si_test")
	for _, step := range [][2]state.AccountStatus{
		{state.AccountActive, state.AccountPastDue},
		{state.AccountPastDue, state.AccountSuspended},
		{state.AccountSuspended, state.AccountDeletedPending},
	} {
		if err := env.store.MarkDunningStep(t.Context(), env.acct.ID, step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/account/restore", nil)
	r.Header.Set("Authorization", "Bearer "+env.key)
	env.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusPaymentRequired || strings.Contains(rec.Body.String(), "lapsed") {
		t.Fatalf("restore of a dunning deletion = %d %s, want 402 naming payment", rec.Code, rec.Body.String())
	}
	if got, _ := env.store.AccountByID(t.Context(), env.acct.ID); got.Status != state.AccountDeletedPending {
		t.Fatalf("status = %s; a refused restore must leave the deletion in place", got.Status)
	}
}
