package events

// The EnsureWake RPC's work before Engine.Wake rides wake.boot_started as
// pre_wake_*_ms, so the gateway's scheduler_wake can be split without a
// gap nobody measures.

import (
	"testing"
	"time"
)

func TestNewPreWakeTimings(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	cases := []struct {
		name                         string
		rpc, ensure, env, lead, wake time.Time
		want                         PreWakeTimings
	}{
		{name: "every step stamped", rpc: at(0), ensure: at(4), env: at(39), lead: at(40), wake: at(52),
			want: PreWakeTimings{Set: true, AuthorizeMs: 4, EnvironmentMs: 35, CoordinateMs: 1, TransitionMs: 12}},
		{name: "not through EnsureWake", ensure: at(4), env: at(39), lead: at(40), wake: at(52), want: PreWakeTimings{}},
		{name: "leader never marked", rpc: at(0), ensure: at(4), env: at(39), wake: at(52), want: PreWakeTimings{}},
		{name: "clock skew clamps to zero", rpc: at(10), ensure: at(4), env: at(39), lead: at(40), wake: at(52),
			want: PreWakeTimings{Set: true, AuthorizeMs: 0, EnvironmentMs: 35, CoordinateMs: 1, TransitionMs: 12}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewPreWakeTimings(tc.rpc, tc.ensure, tc.env, tc.lead, tc.wake)
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			p := BootStarted{WakeID: "w", PreRPC: PreRPCTimings{Set: true, PreWake: got}}.Payload()
			_, present := p["pre_wake_environment_ms"]
			if present != got.Set {
				t.Fatalf("pre_wake_* in payload = %v, want %v", present, got.Set)
			}
			if got.Set && p["pre_wake_environment_ms"] != got.EnvironmentMs {
				t.Fatalf("pre_wake_environment_ms = %v, want %d", p["pre_wake_environment_ms"], got.EnvironmentMs)
			}
		})
	}
}
