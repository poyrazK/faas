// adr: 1151
package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 12 H12-49: a thaw resumes a frozen instance under its original wake_id,
// so the trace view reads the thaws of a request's wake within its window.
func TestWakeThawStores(t *testing.T) {
	for _, backend := range []string{"mem", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			mem := state.NewMemStore()
			var s state.WakeThawsStore = mem
			appendEvent := mem.AppendEventAt
			app, other := uuid.NewString(), uuid.NewString()
			if backend == "postgres" {
				pg := state.NewPgStore(pgtest.OpenMigrated(t))
				s, appendEvent = pg, pg.AppendEventAt
			}
			at := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
			wake, otherWake := uuid.NewString(), uuid.NewString()
			thaw := func(appID, wakeID string, when time.Time, total any) {
				raw, _ := json.Marshal(map[string]any{"app_id": appID, "wake_id": wakeID, "total_ms": total})
				if err := appendEvent(t.Context(), "schedd", "wake.thaw_breakdown", nil, raw, when); err != nil {
					t.Fatal(err)
				}
			}
			thaw(app, wake, at.Add(20*time.Second), 110)
			thaw(app, wake, at.Add(-time.Hour), 90)          // outside the window
			thaw(app, otherWake, at.Add(10*time.Second), 70) // a wake not asked for
			thaw(other, wake, at.Add(30*time.Second), 60)    // another account's app
			thaw(app, wake, at.Add(40*time.Second), "fast")  // a malformed duration reads as 0
			got, err := s.ListWakeThaws(t.Context(), []string{app}, []string{wake}, at, at.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			want := []state.WakeThawRecord{{WakeID: wake, At: at.Add(20 * time.Second), TotalMs: 110}, {WakeID: wake, At: at.Add(40 * time.Second)}}
			if len(got) != len(want) {
				t.Fatalf("thaws = %+v, want %+v", got, want)
			}
			for i := range want {
				if got[i].WakeID != want[i].WakeID || !got[i].At.Equal(want[i].At) || got[i].TotalMs != want[i].TotalMs {
					t.Fatalf("thaw %d = %+v, want %+v", i, got[i], want[i])
				}
			}
			if got, err := s.ListWakeThaws(t.Context(), nil, []string{wake}, at, at.Add(time.Minute)); err != nil || len(got) != 0 {
				t.Fatalf("no apps = %+v, %v", got, err)
			}
			if _, err := s.ListWakeThaws(t.Context(), []string{app}, []string{"not-a-wake"}, at, at.Add(time.Minute)); err == nil {
				t.Fatal("a malformed wake id was accepted")
			}
		})
	}
}
