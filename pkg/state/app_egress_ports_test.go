package state_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-361: extra egress ports round-trip through UpdateApp identically in
// both stores; an empty write clears them and an untouched write keeps them.
func exerciseAppEgressPorts(t *testing.T, ctx context.Context, s state.Store, appID string) {
	t.Helper()
	get := func() []int {
		t.Helper()
		app, err := s.AppByID(ctx, appID)
		if err != nil {
			t.Fatalf("AppByID: %v", err)
		}
		return app.EgressPorts
	}
	if got := get(); len(got) != 0 {
		t.Fatalf("new app has egress ports %v", got)
	}
	updated, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{EgressPorts: []int{5432, 6379}, SetEgressPorts: true})
	if err != nil {
		t.Fatalf("UpdateApp set: %v", err)
	}
	if !reflect.DeepEqual(updated.EgressPorts, []int{5432, 6379}) || !reflect.DeepEqual(get(), []int{5432, 6379}) {
		t.Fatalf("after set: returned %v, stored %v", updated.EgressPorts, get())
	}
	if _, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{MinInstances: ptrInt(0), SetMinInstances: true}); err != nil {
		t.Fatalf("UpdateApp unrelated: %v", err)
	}
	if !reflect.DeepEqual(get(), []int{5432, 6379}) {
		t.Fatalf("an unrelated update changed the ports to %v", get())
	}
	if _, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{EgressPorts: []int{}, SetEgressPorts: true}); err != nil {
		t.Fatalf("UpdateApp clear: %v", err)
	}
	if got := get(); got != nil {
		t.Fatalf("after clear: %v, want nil", got)
	}
}

func TestMemStore_AppEgressPorts(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "egress-ports-mem@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "egress-ports-mem", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 300})
	if err != nil {
		t.Fatal(err)
	}
	exerciseAppEgressPorts(t, ctx, s, app.ID)
}

func TestPgStore_AppEgressPorts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	s := state.NewPgStore(pool)
	exerciseAppEgressPorts(t, ctx, s, seedAppForAllowlist(t, ctx, s, "egress-ports-pg"))
}
