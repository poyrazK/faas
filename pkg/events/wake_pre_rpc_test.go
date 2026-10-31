package events

import (
	"testing"
	"time"
)

// Production-us rc.290: ~157 ms of schedd coordination per restore ran before
// the vmmd RPC with no breakdown. BootStarted carries the per-step spans.
func TestBootStartedPreRPCTimings(t *testing.T) {
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	cases := []struct {
		name                                                       string
		wakeStarted, attempt, locked, admitStart, admitted, inputs time.Time
		want                                                       map[string]int64
		unset                                                      bool
	}{
		{
			name: "through Wake", wakeStarted: base, attempt: at(12), locked: at(13), admitStart: at(44), admitted: at(88), inputs: at(106),
			want: map[string]int64{
				"pre_rpc_before_admission_ms": 12, "pre_rpc_lock_wait_ms": 1, "pre_rpc_reads_ms": 31,
				"pre_rpc_admit_ms": 44, "pre_rpc_post_admit_ms": 18,
			},
		},
		{
			name: "admission called directly", attempt: base, locked: at(2), admitStart: at(10), admitted: at(20), inputs: at(25),
			want: map[string]int64{
				"pre_rpc_before_admission_ms": 0, "pre_rpc_lock_wait_ms": 2, "pre_rpc_reads_ms": 8,
				"pre_rpc_admit_ms": 10, "pre_rpc_post_admit_ms": 5,
			},
		},
		{
			name: "out-of-order instants clamp to zero", attempt: base, locked: at(5), admitStart: at(4), admitted: at(9), inputs: at(9),
			want: map[string]int64{"pre_rpc_reads_ms": 0, "pre_rpc_admit_ms": 5},
		},
		{name: "never admitted", attempt: base, locked: at(1), admitStart: at(2), inputs: at(3), unset: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			timings := NewPreRPCTimings(tc.wakeStarted, tc.attempt, tc.locked, tc.admitStart, tc.admitted, tc.inputs)
			p := BootStarted{WakeID: "w", PreRPC: timings}.Payload()
			if tc.unset {
				if timings.Set {
					t.Fatalf("timings = %+v, want unset", timings)
				}
				if _, ok := p["pre_rpc_admit_ms"]; ok {
					t.Fatal("unset timings must be omitted from the payload")
				}
				return
			}
			for k, v := range tc.want {
				if got, ok := p[k].(int64); !ok || got != v {
					t.Errorf("payload[%q] = %v, want %d", k, p[k], v)
				}
			}
		})
	}
}
