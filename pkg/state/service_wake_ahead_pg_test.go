package state_test

// adr: 956

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
