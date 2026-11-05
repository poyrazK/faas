package state_test

// adr: 977

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceWakeAheadStore(t *testing.T) {
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
			w, ok := s.(state.ServiceWakeAheadStore)
			if !ok {
				t.Fatal("store does not implement ServiceWakeAheadStore")
			}
			a, app, _, d := healthFixture(t, s)
			other, _, _, _ := healthFixture(t, s)
			got, err := w.GetServiceWakeAhead(t.Context(), a.ID, app.ID)
			if err != nil || got.Enabled || !got.UpdatedAt.IsZero() {
				t.Fatalf("default = %+v, %v; want off and never set", got, err)
			}
			if enabled, err := w.ServiceWakeAheadEnabled(t.Context(), app.ID); err != nil || enabled {
				t.Fatalf("enabled = %v, %v", enabled, err)
			}
			set, err := w.SetServiceWakeAhead(t.Context(), a.ID, app.ID, true)
			if err != nil || !set.Enabled || set.UpdatedAt.IsZero() {
				t.Fatalf("set = %+v, %v", set, err)
			}
			if enabled, err := w.ServiceWakeAheadEnabled(t.Context(), app.ID); err != nil || !enabled {
				t.Fatalf("enabled after set = %v, %v", enabled, err)
			}
			if _, err := w.SetServiceWakeAhead(t.Context(), other.ID, app.ID, false); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign set = %v, want ErrNotFound", err)
			}
			if got, err := w.GetServiceWakeAhead(t.Context(), other.ID, app.ID); err != nil || got.Enabled {
				t.Fatalf("foreign read exposed the setting: %+v %v", got, err)
			}
			if got, err := w.GetServiceWakeAhead(t.Context(), a.ID, app.ID); err != nil || !got.Enabled {
				t.Fatalf("foreign set must not change it: %+v %v", got, err)
			}

			// ADR-20261025105812: a production project member under the project
			// policy that never chose has wake-ahead on; an explicit choice,
			// another policy, or a preview keeps the off default.
			project, err := s.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "wake-" + uuid.NewString()[:8]})
			if err != nil {
				t.Fatal(err)
			}
			member := func(slug string, policy api.ServiceBindingPolicy, previewOf string) state.App {
				t.Helper()
				created, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: project.ID, Slug: slug + "-" + uuid.NewString()[:8],
					WorkloadName: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, PreviewOfSlug: previewOf,
					Manifest: state.AppManifest{ServiceBindingPolicy: policy}})
				if err != nil {
					t.Fatal(err)
				}
				return created
			}
			sibling := member("web", api.ServiceBindingPolicyProject, "")
			declared := member("admin", api.ServiceBindingPolicyDeclared, "")
			preview := member("pr", api.ServiceBindingPolicyProject, sibling.Slug)
			for _, tc := range []struct {
				app  state.App
				want bool
			}{{sibling, true}, {declared, false}, {preview, false}} {
				if enabled, err := w.ServiceWakeAheadEnabled(t.Context(), tc.app.ID); err != nil || enabled != tc.want {
					t.Errorf("%s default = %v, %v; want %v", tc.app.Slug, enabled, err, tc.want)
				}
				setting, err := w.GetServiceWakeAhead(t.Context(), a.ID, tc.app.ID)
				if err != nil || state.EffectiveServiceWakeAhead(setting, tc.app) != tc.want {
					t.Errorf("%s effective = %v, %v; want %v", tc.app.Slug, state.EffectiveServiceWakeAhead(setting, tc.app), err, tc.want)
				}
			}
			if _, err := w.SetServiceWakeAhead(t.Context(), a.ID, sibling.ID, false); err != nil {
				t.Fatal(err)
			}
			if enabled, err := w.ServiceWakeAheadEnabled(t.Context(), sibling.ID); err != nil || enabled {
				t.Fatalf("explicit off overridden by the default: %v, %v", enabled, err)
			}

			before, ceiling, err := w.ServiceWakeAheadFleetResidency(t.Context())
			if err != nil || ceiling <= 0 {
				t.Fatalf("residency = %d/%d, %v", before, ceiling, err)
			}
			node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "wake-ahead-" + uuid.NewString(), TargetURL: "unix:///tmp/wake-ahead.sock", VPCPUs: 8,
				MemMB: 4096, MaxConcurrency: 16, AdmissionCeilingMB: 4096, VCPUBudget: 8, Lifecycle: state.NodeLifecycleActive, LastHeartbeatAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateInstance(t.Context(), app.ID, d.ID, string(state.StateRunning), 256, node.ID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			after, _, err := w.ServiceWakeAheadFleetResidency(t.Context())
			if err != nil || after-before != 256+api.PerVMOverheadMB {
				t.Fatalf("residency grew by %d, want %d (%v)", after-before, 256+api.PerVMOverheadMB, err)
			}
		})
	}
}
