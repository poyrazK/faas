package apidserver_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Bug hunt 33 H1: the create route refused Free-plan databases unless compute
// caps are enforced, but Compose and dev databases did not, so a Free account
// got an uncapped database through either.
func TestFreePlanComposeAndDevDatabasesNeedEnforcedCaps(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanFree, "free-db-gate", 9033)
	ctx := t.Context()
	store := *env.Store()
	srv := *env.Srv()
	acct, err := store.AccountByID(ctx, *env.AcctID())
	if err != nil {
		t.Fatal(err)
	}
	apidserver.AttachTestManagedPostgres(t, srv, 3)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "free-dev", RAMMB: 256, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}

	for _, mode := range []managedpostgres.CapEnforcementMode{managedpostgres.CapEnforcementShadow, managedpostgres.CapEnforcementOff} {
		srv.SetManagedPostgresCapModeForTest(mode)
		provided, reason, err := apidserver.ComposePostgresPlanFor(srv, ctx, acct)
		if err != nil || provided || reason != "plan" {
			t.Fatalf("caps %s: Compose database provided=%v reason=%q err=%v; want not provided for the plan", mode, provided, reason, err)
		}
		if _, err := srv.EnsureDevPostgresForTest(ctx, acct, app); !errors.Is(err, managedpostgres.ErrNotInPlan) {
			t.Fatalf("caps %s: dev database err = %v, want ErrNotInPlan", mode, err)
		}
	}

	srv.SetManagedPostgresCapModeForTest(managedpostgres.CapEnforcementOn)
	if provided, reason, err := apidserver.ComposePostgresPlanFor(srv, ctx, acct); err != nil || !provided {
		t.Fatalf("caps on: Compose database provided=%v reason=%q err=%v; want provided", provided, reason, err)
	}
	if _, err := srv.EnsureDevPostgresForTest(ctx, acct, app); errors.Is(err, managedpostgres.ErrNotInPlan) {
		t.Fatalf("caps on: dev database refused for the plan: %v", err)
	}
}

// Bug hunt 33: a downgrade ignored managed PostgreSQL, so every database kept
// running above the new plan's database count.
func TestPlanDowngradeCountsManagedDatabases(t *testing.T) {
	env, srv := apidserver.SetupChangePlan(t, api.PlanPro, "")
	ctx := t.Context()
	pg := apidserver.AttachTestManagedPostgres(t, srv, 10)
	acct := env.Acct()
	pro, _ := api.ManagedPostgresLimitsFor(api.PlanPro)
	hobby, _ := api.ManagedPostgresLimitsFor(api.PlanHobby)
	if pro.DatabasesMax < 2 || hobby.DatabasesMax != 1 {
		t.Skipf("test assumes Pro allows 2+ databases and Hobby 1; got %d and %d", pro.DatabasesMax, hobby.DatabasesMax)
	}
	now := time.Now().UTC()
	for i := range 2 {
		if _, _, err := pg.Reserve(ctx, managedpostgres.Database{
			ID: fmt.Sprintf("db-%d", i), AccountID: acct.ID, Name: fmt.Sprintf("db%d", i), State: managedpostgres.StateProvisioning,
			BackendID: "test", BackendFingerprint: "test", CreatedAt: now,
		}, 10); err != nil {
			t.Fatal(err)
		}
	}
	subject, err := (*env.Store()).AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	problem := srv.PlanDowngradeProblemForTest(ctx, subject, api.PlanHobby)
	if problem == nil || problem.Code != "managed_postgres_not_in_plan" || !strings.Contains(problem.Detail, "delete 1 managed database(s)") {
		t.Fatalf("2 databases on a 1-database plan: problem = %+v", problem)
	}
}
