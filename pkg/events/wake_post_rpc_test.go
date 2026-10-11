package events

import (
	"testing"
	"time"
)

// Production-us hunt #9: schedd's rpc_to_running window measured 130-154 ms
// per wake with no breakdown. BootCompleted carries the per-step spans.
func TestBootCompletedPostRPCTimings(t *testing.T) {
	base := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	timings := NewPostRPCTimings(base, at(3), at(4), at(64), at(90), at(130))
	p := BootCompleted{WakeID: "w", PostRPC: timings}.Payload()
	want := map[string]int64{
		"post_rpc_pre_commit_ms": 3, "post_rpc_stamp_join_ms": 1, "post_rpc_lock_wait_ms": 60,
		"post_rpc_publish_ms": 26, "post_rpc_finalize_ms": 40,
	}
	for k, v := range want {
		if got, ok := p[k].(int64); !ok || got != v {
			t.Errorf("payload[%q] = %v, want %d", k, p[k], v)
		}
	}

	if got := NewPostRPCTimings(time.Time{}, at(1), at(2), at(3), at(4), at(5)); got.Set {
		t.Fatalf("zero rpcEnded produced timings %+v", got)
	}
	if _, ok := (BootCompleted{WakeID: "w"}).Payload()["post_rpc_publish_ms"]; ok {
		t.Fatal("unset timings must be omitted from the payload")
	}
	if got := NewPostRPCTimings(base, at(5), at(4), at(4), at(4), at(4)); got.StampJoinMs != 0 {
		t.Fatalf("out-of-order instants gave a negative span: %+v", got)
	}
}
