package wakelatency_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/wakelatency"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type lookup struct{}

func (lookup) NodeName(id string) string { return map[string]string{"n1": "fsn-1", "n2": "fsn-2"}[id] }
func (lookup) App(id string) (string, string) {
	return map[string]string{"a1": "shop", "a2": "blog"}[id], "acct-" + id
}

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func ev(t *testing.T, kind string, p map[string]any) state.Event {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return state.Event{Kind: kind, Actor: "schedd", At: t0, Data: raw}
}

// completed is a wake.boot_completed row lasting ms.
func completed(t *testing.T, wake, app, node, method string, ms int, extra map[string]any) state.Event {
	p := map[string]any{"wake_id": wake, "instance_id": "i-" + wake, "app_id": app, "node_id": node, "method": method,
		"started_at": t0.Format(time.RFC3339Nano), "completed_at": t0.Add(time.Duration(ms) * time.Millisecond).Format(time.RFC3339Nano)}
	for k, v := range extra {
		p[k] = v
	}
	return ev(t, events.WakeBootCompleted, p)
}

func startedRow(t *testing.T, wake, method string, extra map[string]any) state.Event {
	p := map[string]any{"wake_id": wake, "instance_id": "i-" + wake, "method": method}
	for k, v := range extra {
		p[k] = v
	}
	return ev(t, events.WakeBootStarted, p)
}

func fixture(t *testing.T) map[string][]state.Event {
	return map[string][]state.Event{
		events.WakeBootStarted: {
			startedRow(t, "w3", "cold_boot", map[string]any{"cold_reason": "no_snapshot", "pre_rpc_lock_wait_ms": 40}),
			startedRow(t, "w4", "restore", nil),
			startedRow(t, "w6", "cold_boot", map[string]any{"cold_reason": "stale", "trigger": "platform.snapshot_refresh"}),
		},
		events.WakeBootCompleted: {
			completed(t, "w1", "a1", "n1", "restore", 120, map[string]any{"post_rpc_publish_ms": 9}),
			completed(t, "w2", "a1", "n1", "restore", 500, nil),
			completed(t, "w3", "a2", "n2", "cold_boot", 2400, nil),
			completed(t, "w4", "a2", "n2", "cold_boot", 1800, map[string]any{"restore_fallback_reason": "after_restore_failed"}),
			completed(t, "w6", "a2", "n2", "cold_boot", 3000, map[string]any{"trigger": "platform.snapshot_refresh"}),
		},
		events.WakeRestoreBreakdown: {
			ev(t, events.WakeRestoreBreakdown, map[string]any{"wake_id": "w2", "instance_id": "i-w2", "load_snapshot_ms": 310, "chroot_ms": 20, "total_ms": 480}),
		},
		events.WakeColdBootBreakdown: {
			ev(t, events.WakeColdBootBreakdown, map[string]any{"wake_id": "w3", "instance_id": "i-w3", "wait_ready_ms": 2100, "total_ms": 2300}),
		},
		events.WakeThawBreakdown: {
			ev(t, events.WakeThawBreakdown, map[string]any{"wake_id": "w5", "instance_id": "i-w5", "app_id": "a1", "node_id": "n1", "vmmd_thaw_ms": 8, "total_ms": 15}),
		},
		events.WakeBootFailed: {
			ev(t, events.WakeBootFailed, map[string]any{"wake_id": "w7", "instance_id": "i-w7", "app_id": "a1", "node_id": "n1", "reason": "vmm_start_failed"}),
		},
	}
}

func TestBuildFleetReport(t *testing.T) {
	r := wakelatency.Build(wakelatency.Input{Window: "1h", Since: t0.Add(-time.Hour), Now: t0, Events: fixture(t)}, lookup{})

	want := api.ObsWakeTotals{Wakes: 5, Restores: 2, ColdBoots: 2, Thaws: 1, Failures: 1, Fallbacks: 1, RestoresOverTarget: 1, PlatformRefreshes: 1}
	if r.Totals != want {
		t.Fatalf("totals = %+v, want %+v", r.Totals, want)
	}
	if len(r.Methods) != 3 || r.Methods[0].Method != "restore" || r.Methods[0].P50MS != 120 || r.Methods[0].MaxMS != 500 || r.Methods[0].OverTarget != 1 {
		t.Fatalf("methods = %+v", r.Methods)
	}
	if r.Methods[1].Method != "cold_boot" || r.Methods[2].Method != "thaw" || r.Methods[2].MaxMS != 15 {
		t.Fatalf("method order = %+v", r.Methods)
	}
	if len(r.Nodes) != 2 || r.Nodes[0].NodeName != "fsn-1" || r.Nodes[0].RestoreP95MS != 500 || r.Nodes[0].Failures != 1 || r.Nodes[0].Thaws != 1 {
		t.Fatalf("nodes = %+v", r.Nodes)
	}
	if r.Nodes[1].ColdBoots != 2 || r.Nodes[1].Fallbacks != 1 {
		t.Fatalf("node fsn-2 = %+v", r.Nodes[1])
	}
	if r.Phases[0].Method != "cold_boot" || r.Phases[0].Phase != "wait_ready" || r.Phases[0].P95MS != 2100 {
		t.Fatalf("slowest phase = %+v", r.Phases[0])
	}
	phases := map[string]bool{}
	for _, p := range r.Phases {
		phases[p.Method+"/"+p.Phase] = true
	}
	for _, name := range []string{"restore/load_snapshot", "restore/chroot", "schedd/pre_rpc_lock_wait", "schedd/post_rpc_publish", "thaw/vmmd_thaw"} {
		if !phases[name] {
			t.Errorf("phase %s missing from %v", name, phases)
		}
	}
	if phases["restore/total"] {
		t.Error("total_ms reported as a phase")
	}
	if len(r.ColdReasons) != 2 || r.ColdReasons[0] != (api.ObsWakeReasonCount{Reason: "no_snapshot", Count: 1}) || r.ColdReasons[1].Reason != "restore_fallback" {
		t.Fatalf("cold reasons = %+v", r.ColdReasons)
	}
	if len(r.FailureReasons) != 1 || r.FailureReasons[0].Reason != "vmm_start_failed" {
		t.Fatalf("failure reasons = %+v", r.FailureReasons)
	}
	top := r.Slowest[0]
	if top.WakeID != "w3" || top.DurationMS != 2400 || top.AppSlug != "blog" || top.NodeName != "fsn-2" || top.SlowPhase != "cold_boot/wait_ready" || top.SlowPhaseMS != 2100 || top.ColdReason != "no_snapshot" {
		t.Fatalf("slowest = %+v", top)
	}
	if len(r.ColdBootApps) != 1 || r.ColdBootApps[0].AppSlug != "blog" || r.ColdBootApps[0].ColdBoots != 2 || r.ColdBootApps[0].Fallbacks != 1 {
		t.Fatalf("cold boot apps = %+v", r.ColdBootApps)
	}
}

func TestBuildFilters(t *testing.T) {
	cases := []struct {
		name         string
		node, app    string
		wakes, fails int
	}{
		{"node", "n1", "", 3, 1},
		{"app", "", "a2", 2, 0},
		{"both", "n2", "a1", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := wakelatency.Build(wakelatency.Input{Window: "1h", Now: t0, Events: fixture(t), NodeID: c.node, AppID: c.app}, lookup{})
			if r.Totals.Wakes != c.wakes || r.Totals.Failures != c.fails {
				t.Fatalf("totals = %+v", r.Totals)
			}
		})
	}
}

func TestBuildEmpty(t *testing.T) {
	r := wakelatency.Build(wakelatency.Input{Window: "1h", Now: t0}, lookup{})
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"methods", "nodes", "phases", "cold_reasons", "failure_reasons", "slowest", "cold_boot_apps"} {
		if _, ok := back[k].([]any); !ok {
			t.Errorf("%s = %v, want an empty list", k, back[k])
		}
	}
}
