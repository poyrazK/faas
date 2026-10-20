// adr: 1638
package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// The trace view reads specific wakes: one record per wake (its first
// instance), only for the requested apps, with the same canonical boot and
// first completion as the wake history page.
func TestWakeHistoryByWakeIDsStores(t *testing.T) {
	for _, backend := range []string{"mem", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			mem := NewMemStore()
			var s WakeHistoryByWakeIDsStore = mem
			at := time.Date(2026, 10, 19, 10, 0, 0, 123456000, time.UTC)
			app, other := uuid.NewString(), uuid.NewString()
			seed := func(id, appID, wake string, when time.Time) {
				mem.instances[id] = Instance{ID: id, AppID: appID, WakeID: wake, State: "running", StartedAt: when}
			}
			appendEvent := mem.AppendEventAt
			if backend == "postgres" {
				pool := pgtest.OpenMigrated(t)
				pg := NewPgStore(pool)
				s = pg
				appendEvent = pg.AppendEventAt
				acct, err := pg.CreateAccount(t.Context(), "history-by-wake@example.test", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range []*string{&app, &other} {
					a, err := pg.CreateApp(t.Context(), App{AccountID: acct.ID, Slug: "history-by-wake-" + uuid.NewString()[:8], Status: AppActive})
					if err != nil {
						t.Fatal(err)
					}
					*p = a.ID
				}
				seed = func(id, appID, wake string, when time.Time) {
					_, err := pool.Exec(t.Context(), `INSERT INTO instances(id,app_id,wake_id,state,ram_mb,node_id,started_at) VALUES($1,$2,$3,'running',128,(SELECT id FROM compute_nodes WHERE name='default-local' LIMIT 1),$4)`, id, appID, wake, when)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			restored, unfinished, foreign, unknown := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			first := "00000000-0000-0000-0000-000000000001"
			seed(first, app, restored, at)
			seed("00000000-0000-0000-0000-000000000002", app, restored, at.Add(time.Second)) // a fan-out sibling
			seed("00000000-0000-0000-0000-000000000003", app, unfinished, at.Add(2*time.Second))
			seed("00000000-0000-0000-0000-000000000004", other, foreign, at)
			event := func(kind, appID, wake string, when time.Time, extra map[string]any) {
				data := map[string]any{"app_id": appID, "wake_id": wake}
				for k, v := range extra {
					data[k] = v
				}
				raw, _ := json.Marshal(data)
				if err := appendEvent(t.Context(), "schedd", kind, nil, raw, when); err != nil {
					t.Fatal(err)
				}
			}
			event("wake.boot_started", app, restored, at, map[string]any{"method": "restore", "trigger": "service.mesh"})
			event("wake.boot_completed", app, restored, at.Add(610*time.Millisecond), nil)
			event("wake.boot_started", app, unfinished, at.Add(2*time.Second), map[string]any{"method": "cold_boot"})

			rows, err := s.ListWakeHistoryByWakeIDs(t.Context(), []string{app}, []string{restored, unfinished, foreign, unknown})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 || rows[0].WakeID != restored || rows[0].InstanceID != first || rows[1].WakeID != unfinished {
				t.Fatalf("rows = %+v, want the first instance of each of the app's wakes", rows)
			}
			if !rows[0].HasMeta || rows[0].CompletedAt.Sub(rows[0].BootAt) != 610*time.Millisecond {
				t.Fatalf("restored wake = %+v, want boot and completion", rows[0])
			}
			if !rows[1].HasMeta || !rows[1].CompletedAt.IsZero() {
				t.Fatalf("unfinished wake = %+v, want a boot and no completion", rows[1])
			}
			if rows, err := s.ListWakeHistoryByWakeIDs(t.Context(), nil, []string{restored}); err != nil || len(rows) != 0 {
				t.Fatalf("no apps = %+v, %v", rows, err)
			}
			if _, err := s.ListWakeHistoryByWakeIDs(t.Context(), []string{app}, []string{"not-a-wake"}); err == nil {
				t.Fatal("a malformed wake id was accepted")
			}
		})
	}
}
