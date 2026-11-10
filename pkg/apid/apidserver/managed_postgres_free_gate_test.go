package apidserver_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Bug hunt 33 H1: the create route refused Free-plan databases unless compute
// caps were enforced, but Compose and dev databases did not, so a Free account
// got an uncapped database through either. ADR-20261110184700: Free includes
// no managed PostgreSQL, so every path refuses it whatever the cap mode.
func TestFreePlanComposeAndDevDatabasesAreNotInPlan(t *testing.T) {
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

	for _, mode := range []managedpostgres.CapEnforcementMode{managedpostgres.CapEnforcementShadow, managedpostgres.CapEnforcementOff, managedpostgres.CapEnforcementOn} {
		srv.SetManagedPostgresCapModeForTest(mode)
		provided, reason, err := apidserver.ComposePostgresPlanFor(srv, ctx, acct)
		if err != nil || provided || reason != "plan" {
			t.Fatalf("caps %s: Compose database provided=%v reason=%q err=%v; want not provided for the plan", mode, provided, reason, err)
		}
		if _, err := srv.EnsureDevPostgresForTest(ctx, acct, app); !errors.Is(err, managedpostgres.ErrNotInPlan) {
			t.Fatalf("caps %s: dev database err = %v, want ErrNotInPlan", mode, err)
		}
	}
}
