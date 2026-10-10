package state_test

// adr: 947

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRoutePriorityStore(t *testing.T) {
	stores := map[string]func(t *testing.T) state.Store{
		"mem": func(*testing.T) state.Store { return state.NewMemStore() },
		"pg": func(t *testing.T) state.Store {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			return state.NewPgStore(pool)
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			p, ok := s.(state.RoutePriorityStore)
			if !ok {
				t.Fatal("store does not implement RoutePriorityStore")
			}
			a, app, _, _ := healthFixture(t, s)
			other, _, _, _ := healthFixture(t, s)

			source, eff, err := state.EffectiveRoutePriorities(t.Context(), s, a.ID, app.ID)
			if err != nil || source != api.RoutePrioritySourceNone || len(eff.Routes) != 0 {
				t.Fatalf("default = %s %+v %v, want none", source, eff, err)
			}
			zero := int64(0)
			if _, err := s.(state.RouteHealthStore).SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{
				Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}},
			}); err != nil {
				t.Fatal(err)
			}
			source, eff, err = state.EffectiveRoutePriorities(t.Context(), s, a.ID, app.ID)
			if err != nil || source != api.RoutePrioritySourceRouteHealth || len(eff.Routes) != 1 ||
				eff.Routes[0] != (api.RoutePriorityRule{Method: "POST", Path: "/checkout", Class: api.RoutePriorityCritical}) {
				t.Fatalf("route-health default = %s %+v %v", source, eff, err)
			}

			rules := []api.RoutePriorityRule{{Path: "/exports/*", Class: api.RoutePriorityBulk}, {Method: "GET", Path: "/users/{id}", Class: api.RoutePriorityCritical}}
			saved, err := p.SetRoutePriorities(t.Context(), a.ID, app.ID, rules)
			if err != nil || !saved.Configured || len(saved.Routes) != 2 || saved.UpdatedAt.IsZero() {
				t.Fatalf("set = %+v, %v", saved, err)
			}
			source, eff, err = state.EffectiveRoutePriorities(t.Context(), s, a.ID, app.ID)
			if err != nil || source != api.RoutePrioritySourceConfigured || len(eff.Routes) != 2 || eff.Routes[1].Path != "/users/{id}" {
				t.Fatalf("configured = %s %+v %v", source, eff, err)
			}
			if _, err := p.SetRoutePriorities(t.Context(), a.ID, app.ID, []api.RoutePriorityRule{}); err != nil {
				t.Fatal(err)
			}
			if source, _, _ := state.EffectiveRoutePriorities(t.Context(), s, a.ID, app.ID); source != api.RoutePrioritySourceConfigured {
				t.Fatalf("an empty saved list must stay configured and override route health, got %s", source)
			}
			if _, err := p.SetRoutePriorities(t.Context(), other.ID, app.ID, rules); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign set = %v, want ErrNotFound", err)
			}
			if got, err := p.GetRoutePriorities(t.Context(), other.ID, app.ID); err != nil || got.Configured {
				t.Fatalf("foreign read exposed priorities: %+v %v", got, err)
			}
			if _, err := p.SetRoutePriorities(t.Context(), a.ID, app.ID, []api.RoutePriorityRule{{Path: "nope", Class: "critical"}}); err == nil {
				t.Fatal("invalid rules must be rejected")
			}
			if err := p.DeleteRoutePriorities(t.Context(), a.ID, app.ID); err != nil {
				t.Fatal(err)
			}
			if source, _, _ := state.EffectiveRoutePriorities(t.Context(), s, a.ID, app.ID); source != api.RoutePrioritySourceRouteHealth {
				t.Fatalf("after delete source = %s, want route_health", source)
			}
		})
	}
}
