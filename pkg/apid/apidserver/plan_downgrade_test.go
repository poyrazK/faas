package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 17 H17-4, spec §10: a downgrade runs the target plan's quota checks
// first and is refused with a task list while the apps do not fit. Before,
// it was scheduled with the provider and every app kept running above the
// new plan, because limits are only checked when an app is written.
func TestPlanDowngradeRefusedUntilAppsFit(t *testing.T) {
	hobby := api.MustLimitsFor(api.PlanHobby)
	for _, tc := range []struct {
		name      string
		apps      int
		bigRAM    bool
		wantCode  string
		wantTasks []string
	}{
		{name: "too many apps and too much RAM", apps: hobby.DeployedApps + 1, bigRAM: true, wantCode: api.CodePlanLimitApps,
			wantTasks: []string{"delete 1 app(s)", "reduce app-0's RAM"}},
		{name: "too much RAM", apps: 1, bigRAM: true, wantCode: api.CodePlanLimitRAM, wantTasks: []string{"reduce app-0's RAM"}},
		// Fits: the request reaches the provider confirmation step, which this
		// box without a provider answers with the billing portal.
		{name: "fits", apps: hobby.DeployedApps, wantCode: api.CodePayment},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := apidserver.SetupChangePlan(t, api.PlanPro, "si_downgrade")
			store := *env.Store()
			for i := range tc.apps {
				ram := hobby.RAMMB
				if tc.bigRAM && i == 0 {
					ram = hobby.RAMMB * 2
				}
				if _, err := store.CreateApp(t.Context(), state.App{AccountID: env.Acct().ID, Slug: fmt.Sprintf("app-%d", i), RAMMB: ram, Type: state.AppTypeApp}); err != nil {
					t.Fatal(err)
				}
			}
			rec := env.Do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": string(api.PlanHobby)}, nil)
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatalf("PATCH /v1/account/plan = %d %s", rec.Code, rec.Body.String())
			}
			if problem.Code != tc.wantCode {
				t.Fatalf("PATCH /v1/account/plan = %d code %q (%s), want %q", rec.Code, problem.Code, problem.Detail, tc.wantCode)
			}
			for _, task := range tc.wantTasks {
				if !strings.Contains(problem.Detail, task) {
					t.Fatalf("detail %q lacks task %q", problem.Detail, task)
				}
			}
			acct, err := store.AccountByID(t.Context(), env.Acct().ID)
			if err != nil {
				t.Fatal(err)
			}
			if acct.Plan != api.PlanPro {
				t.Fatalf("plan = %s, want pro unchanged", acct.Plan)
			}
		})
	}
}

// An organization's downgrade counts the organization's apps, not its
// billing contact's personal ones.
func TestPlanDowngradeCountsOrganizationApps(t *testing.T) {
	env, srv := apidserver.SetupChangePlan(t, api.PlanPro, "")
	store := *env.Store()
	ctx := t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "downgrade-co", Name: "Downgrade Co", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := store.CreateApp(ctx, state.App{AccountID: env.Acct().ID, OrgID: org.ID, Slug: fmt.Sprintf("org-app-%d", i), RAMMB: 256, Type: state.AppTypeApp}); err != nil {
			t.Fatal(err)
		}
	}
	subject, err := state.BillingResourceAccount(ctx, store, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	free := api.MustLimitsFor(api.PlanFree)
	problem := srv.PlanDowngradeProblemForTest(ctx, subject, api.PlanFree)
	if problem == nil || problem.Code != api.CodePlanLimitApps || !strings.Contains(problem.Detail, fmt.Sprintf("you have %d", 2)) {
		t.Fatalf("organization with 2 apps on a %d-app plan: problem = %+v", free.DeployedApps, problem)
	}
	personal, err := store.AccountByID(ctx, env.Acct().ID)
	if err != nil {
		t.Fatal(err)
	}
	if problem := srv.PlanDowngradeProblemForTest(ctx, personal, api.PlanFree); problem != nil {
		t.Fatalf("the contact's personal apps are empty, yet: %+v", problem)
	}
}
