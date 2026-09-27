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
	raw, err := mintDashboardSession(t.Context(), store, mgr, acct.ID)
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

// TestPastDueAccount_CannotDeploy — spec §4.7: past_due means "apps run,
// deploys blocked", and both dunning emails promise it. Account.Active
// admits past_due so its apps keep serving, and no deploy path checked
// anything stricter: a past_due account deployed with a 202.
func TestPastDueAccount_CannotDeploy(t *testing.T) {
	env, _ := setupChangePlan(t, api.PlanHobby, "si_test")
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+env.key)
		r.Header.Set("Content-Type", "application/json")
		env.h.ServeHTTP(rec, r)
		return rec
	}
	if rec := do("POST", "/v1/apps", `{"slug":"pd-app","runtime":"node22"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create app = %d %s", rec.Code, rec.Body.String())
	}
	if err := env.store.MarkDunningStep(t.Context(), env.acct.ID, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	deploy := `{"image":"registry.gregale.dev/app@sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"}`
	rec := do("POST", "/v1/apps/pd-app/deployments", deploy)
	if rec.Code != http.StatusPaymentRequired || !strings.Contains(rec.Body.String(), api.CodeBillingPastDue) {
		t.Fatalf("past_due deploy = %d %s, want 402 %s", rec.Code, rec.Body.String(), api.CodeBillingPastDue)
	}
	// Reads and serving continue during the grace period.
	if rec := do("GET", "/v1/apps/pd-app", ""); rec.Code != http.StatusOK {
		t.Fatalf("past_due app read = %d, want 200", rec.Code)
	}
	// Paying lifts the block.
	if err := env.store.UpdateAccountStatus(t.Context(), env.acct.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	if rec := do("POST", "/v1/apps/pd-app/deployments", deploy); rec.Code != http.StatusAccepted {
		t.Fatalf("deploy after paying = %d %s, want 202", rec.Code, rec.Body.String())
	}
}

// TestDeleteAccount_PastDueGetsAReason — the store refuses to schedule a
// past_due account's self-service deletion, and the handler reported that
// refusal as a 503 "could not mark for deletion", which a customer
// retries forever. It is a 409 that names the blocker.
func TestDeleteAccount_PastDueGetsAReason(t *testing.T) {
	env, _ := setupChangePlan(t, api.PlanHobby, "si_test")
	if err := env.store.MarkDunningStep(t.Context(), env.acct.ID, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/v1/account", nil)
	r.Header.Set("Authorization", "Bearer "+env.key)
	env.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "outstanding payment") {
		t.Fatalf("DELETE /v1/account on past_due = %d %s, want 409 naming the outstanding payment", rec.Code, rec.Body.String())
	}
	if got, _ := env.store.AccountByID(t.Context(), env.acct.ID); got.Status != state.AccountPastDue || got.DeletionRequestedAt != nil {
		t.Fatalf("after refused delete: status=%s deletion_requested_at=%v", got.Status, got.DeletionRequestedAt)
	}
}

// TestRestoreApp_HonoursDeployedAppQuota — restoring a deleted app did not
// count it against the plan: on Free (one app), delete A → create B →
// restore A left two live apps, and repeating the cycle had no ceiling.
func TestRestoreApp_HonoursDeployedAppQuota(t *testing.T) {
	env, _ := setupChangePlan(t, api.PlanFree, "")
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+env.key)
		r.Header.Set("Content-Type", "application/json")
		env.h.ServeHTTP(rec, r)
		return rec
	}
	for _, step := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/v1/apps", `{"slug":"quota-a","runtime":"node22"}`, http.StatusCreated},
		{"DELETE", "/v1/apps/quota-a", "", http.StatusNoContent},
		{"POST", "/v1/apps", `{"slug":"quota-b","runtime":"node22"}`, http.StatusCreated},
		{"POST", "/v1/apps/quota-a/restore", "", http.StatusForbidden},
		{"DELETE", "/v1/apps/quota-b", "", http.StatusNoContent},
		{"POST", "/v1/apps/quota-a/restore", "", http.StatusOK},
	} {
		rec := do(step.method, step.path, step.body)
		if rec.Code != step.want {
			t.Fatalf("%s %s = %d %s, want %d", step.method, step.path, rec.Code, rec.Body.String(), step.want)
		}
		if step.want == http.StatusForbidden && !strings.Contains(rec.Body.String(), "plan_limit_apps") {
			t.Fatalf("over-quota restore body = %s, want plan_limit_apps", rec.Body.String())
		}
	}
}
