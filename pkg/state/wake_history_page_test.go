// adr: 1638
package state

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestWakeHistoryPageStores(t *testing.T) {
	for _, backend := range []string{"mem", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			mem := NewMemStore()
			var s WakeHistoryPageStore = mem
			at := time.Date(2026, 10, 16, 10, 0, 0, 123456000, time.UTC)
			app, wake := uuid.NewString(), uuid.NewString()
			seed := func(id string, when time.Time) {
				mem.instances[id] = Instance{ID: id, AppID: app, WakeID: wake, State: "running", StartedAt: when}
			}
			appendEvent := mem.AppendEventAt
			if backend == "postgres" {
				pool := pgtest.OpenMigrated(t)
				pg := NewPgStore(pool)
				s = pg
				appendEvent = pg.AppendEventAt
				acct, err := pg.CreateAccount(t.Context(), "history-page@example.test", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				a, err := pg.CreateApp(t.Context(), App{AccountID: acct.ID, Slug: "history-page", Status: AppActive})
				if err != nil {
					t.Fatal(err)
				}
				app = a.ID
				seed = func(id string, when time.Time) {
					var value any = when
					if when.IsZero() {
						value = nil
					}
					_, err := pool.Exec(t.Context(), `INSERT INTO instances(id,app_id,wake_id,state,ram_mb,node_id,started_at) VALUES($1,$2,$3,'running',128,(SELECT id FROM compute_nodes WHERE name='default-local' LIMIT 1),$4)`, id, app, wake, value)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			for i := 0; i < 145; i++ {
				when := at
				if i >= 5 {
					when = at.Add(24 * time.Hour)
				}
				seed(fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1), when)
			}
			seed(uuid.NewString(), time.Time{})
			for _, e := range []struct{ app, actor, trigger string }{{"foreign", "schedd", "foreign"}, {app, "vmmd", "mirror"}, {app, "schedd", "owned"}} {
				data, _ := json.Marshal(map[string]any{"app_id": e.app, "wake_id": wake, "trigger": e.trigger, "at_capacity": false})
				if err := appendEvent(t.Context(), e.actor, "wake.boot_started", nil, data, at); err != nil {
					t.Fatal(err)
				}
			}
			for _, owner := range []string{"foreign", app} {
				data, _ := json.Marshal(map[string]string{"app_id": owner, "wake_id": wake})
				completed := at.Add(65500 * time.Millisecond)
				if owner == "foreign" {
					completed = at.Add(time.Millisecond)
				}
				if err := appendEvent(t.Context(), "vmmd", "wake.boot_completed", nil, data, completed); err != nil {
					t.Fatal(err)
				}
			}
			var after WakeHistoryPosition
			seen := map[string]bool{}
			for page := 0; page < 3; page++ {
				rows, err := s.ListAppWakeHistoryPage(t.Context(), app, at, at.Add(time.Hour), after, 2)
				if err != nil {
					t.Fatal(err)
				}
				want := 2
				if page == 2 {
					want = 1
				}
				if len(rows) != want {
					t.Fatalf("historical page %d rows=%d", page, len(rows))
				}
				for _, r := range rows {
					if seen[r.InstanceID] || r.AppID != app || !r.StartedAt.Equal(at) || after.ID != "" && r.InstanceID >= after.ID || !r.HasMeta || !r.CompletedAt.Equal(at.Add(65500*time.Millisecond)) {
						t.Fatal("lost order, scope or canonical metadata")
					}
					var data map[string]any
					if json.Unmarshal(r.BootData, &data) != nil || data["trigger"] != "owned" {
						t.Fatal("foreign/mirror metadata selected")
					}
					seen[r.InstanceID] = true
					after = WakeHistoryPosition{At: r.StartedAt, ID: r.InstanceID}
				}
			}
			rows, err := s.ListAppWakeHistoryPage(t.Context(), app, at, at.Add(time.Hour), after, 2)
			if err != nil || len(rows) != 0 || len(seen) != 5 {
				t.Fatal("terminal page repeated or skipped tied records")
			}
			for _, limit := range []int{-1, 0, 102} {
				if _, err := s.ListAppWakeHistoryPage(t.Context(), app, at, at.Add(time.Hour), WakeHistoryPosition{}, limit); err == nil {
					t.Fatal("invalid bound accepted")
				}
			}
		})
	}
}
