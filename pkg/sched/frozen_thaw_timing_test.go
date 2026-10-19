// adr: 1151 — every thaw of a frozen VM reports where schedd spent its time,
// and keeps the instance's wake_id (its incarnation) while doing so.
package sched

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// thawBreakdowns waits until want wake.thaw_breakdown rows exist for wakeID
// and returns their payloads, oldest first. EmitAsync writes on a worker.
func thawBreakdowns(t *testing.T, store state.Store, wakeID string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := store.ListEventsByWakeID(context.Background(), wakeID, time.Time{}, 0)
		if err != nil {
			t.Fatalf("ListEventsByWakeID: %v", err)
		}
		var out []map[string]any
		for _, row := range rows {
			if row.Kind != events.WakeThawBreakdown {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal(row.Data, &payload); err != nil {
				t.Fatalf("decode %s: %v", row.Kind, err)
			}
			out = append(out, payload)
		}
		if len(out) >= want {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("thaw breakdown rows for %s = %d, want %d", wakeID, len(out), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEachThawEmitsItsOwnBreakdown(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	f.e.WithEvents(events.NewPlatform("schedd", f.store, testLog(), nil, nil))
	for thaw := 1; thaw <= 2; thaw++ {
		if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled {
			t.Fatalf("FreezeIdle before thaw %d: %v, %v", thaw, handled, err)
		}
		result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
		if err != nil || result.InstanceID != f.ins.ID {
			t.Fatalf("thaw %d: result %+v, err %v", thaw, result, err)
		}
		// Exclusive work and the config proof match instances.wake_id, so a
		// thaw answers with the incarnation's wake_id rather than a new one.
		if result.WakeID != f.ins.WakeID {
			t.Fatalf("thaw %d answered wake_id %q, want the instance's %q", thaw, result.WakeID, f.ins.WakeID)
		}
		rows := thawBreakdowns(t, f.store, f.ins.WakeID, thaw)
		latest := rows[len(rows)-1]
		if latest["instance_id"] != f.ins.ID || latest["app_id"] != f.app.ID || latest["node_id"] != f.ins.NodeID {
			t.Fatalf("thaw %d breakdown identity = %v", thaw, latest)
		}
		total, _ := latest["total_ms"].(float64)
		var spans float64
		for _, key := range []string{"wake_reads_ms", "thaw_reads_ms", "vmmd_thaw_ms", "publish_ms", "after_publish_ms"} {
			ms, ok := latest[key].(float64)
			if !ok || ms < 0 {
				t.Fatalf("thaw %d breakdown %s = %v", thaw, key, latest[key])
			}
			spans += ms
		}
		// Spans are truncated to whole milliseconds separately, so their sum
		// may fall short of the total by at most one per span.
		if spans > total || total-spans > 5+1 {
			t.Fatalf("thaw %d spans sum to %v ms, total %v ms", thaw, spans, total)
		}
	}
}

func TestThawBreakdownSpansAreConsecutive(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	for _, tc := range []struct {
		name   string
		timing thawTiming
		answer time.Time
		want   map[string]int64
	}{
		{
			name:   "measured thaw",
			timing: thawTiming{wakeStarted: at(0), thawTried: at(80), rpcStarted: at(111), rpcEnded: at(115), published: at(127)},
			answer: at(136),
			want: map[string]int64{"wake_reads_ms": 80, "thaw_reads_ms": 31, "vmmd_thaw_ms": 4,
				"publish_ms": 12, "after_publish_ms": 9, "total_ms": 136},
		},
		{
			name:   "missing wake start reports zero, not a negative span",
			timing: thawTiming{thawTried: at(5), rpcStarted: at(9), rpcEnded: at(12), published: at(20)},
			answer: at(25),
			want: map[string]int64{"wake_reads_ms": 0, "thaw_reads_ms": 4, "vmmd_thaw_ms": 3,
				"publish_ms": 8, "after_publish_ms": 5, "total_ms": 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			e := &Engine{log: testLog()}
			e.WithEvents(events.NewPlatform("schedd", store, testLog(), nil, nil))
			ins := state.Instance{ID: "00000000-0000-4000-8000-0000000000a1", WakeID: "00000000-0000-4000-8000-0000000000b1", NodeID: "node-1"}
			e.emitThawBreakdown(t.Context(), "app-1", ins, tc.timing, tc.answer)
			payload := thawBreakdowns(t, store, ins.WakeID, 1)[0]
			for key, want := range tc.want {
				if got, _ := payload[key].(float64); int64(got) != want {
					t.Errorf("%s = %v, want %d", key, payload[key], want)
				}
			}
		})
	}
}
