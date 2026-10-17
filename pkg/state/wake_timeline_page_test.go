// adr: 1637
package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type wakePageTestStore interface {
	WakeTimelinePageStore
	AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
}

func TestWakeTimelinePageStores(t *testing.T) {
	for _, backend := range []string{"mem", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store wakePageTestStore = NewMemStore()
			if backend == "postgres" {
				store = NewPgStore(pgtest.OpenMigrated(t))
			}
			app, wake := uuid.NewString(), uuid.NewString()
			at := time.Date(2026, 10, 16, 10, 0, 0, 123456789, time.UTC)
			for _, row := range []struct{ app, actor, kind string }{
				{"foreign", "schedd", "wake.boot_started"}, {app, "vmmd", "wake.boot_started"}, {app, "schedd", "wake.queue_accepted"}, {app, "schedd", "wake.admitted"}, {app, "schedd", "wake.boot_started"}, {app, "schedd", "wake.boot_completed"}, {"foreign", "schedd", "wake.admitted"}, {app, "gatewayd", "wake.proxy_first_byte"}, {app, "gatewayd", "wake.proxy_first_byte"}, {app, "vmmd", "wake.boot_started"},
			} {
				raw, _ := json.Marshal(map[string]string{"app_id": row.app, "wake_id": wake})
				if err := store.AppendEventAt(t.Context(), row.actor, row.kind, nil, raw, at); err != nil {
					t.Fatal(err)
				}
			}
			at = at.Truncate(time.Microsecond)
			var pos WakeTimelinePosition
			seen := map[int64]bool{}
			kinds := []string{}
			for page := 0; page < 4; page++ {
				rows, err := store.ListAppWakeTimelinePage(t.Context(), app, wake, time.Time{}, pos, 2)
				if err != nil {
					t.Fatal(err)
				}
				if page == 3 {
					if len(rows) != 0 {
						t.Fatal("terminal page repeated events")
					}
					break
				}
				if len(rows) != 2 {
					t.Fatalf("page %d omitted owned events after forge filtering: %d", page, len(rows))
				}
				for _, e := range rows {
					if seen[e.ID] || !e.At.Equal(at) || e.ID <= pos.ID || e.Kind == "wake.boot_started" && e.Actor != "schedd" {
						t.Fatal("tied-time seek duplicated/skipped canonical events")
					}
					seen[e.ID] = true
					kinds = append(kinds, e.Kind)
					pos = WakeTimelinePosition{At: e.At, ID: e.ID}
				}
			}
			want := []string{"wake.queue_accepted", "wake.admitted", "wake.boot_started", "wake.boot_completed", "wake.proxy_first_byte", "wake.proxy_first_byte"}
			if len(kinds) != len(want) {
				t.Fatal("wrong owner event count")
			}
			for i := range want {
				if kinds[i] != want[i] {
					t.Fatal("wrong total order/canonical selection")
				}
			}
			rows, err := store.ListAppWakeTimelinePage(t.Context(), app, wake, at, WakeTimelinePosition{}, 2)
			if err != nil || len(rows) != 0 {
				t.Fatal("since must remain exclusive")
			}
			for _, limit := range []int{0, -1, 1002} {
				if _, err := store.ListAppWakeTimelinePage(t.Context(), app, wake, time.Time{}, WakeTimelinePosition{}, limit); err == nil {
					t.Fatal("unbounded page accepted")
				}
			}
		})
	}
}
