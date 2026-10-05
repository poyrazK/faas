// adr: 064
// spec: §6.3
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Exercise the production readiness loop and persisted timeline row, not a
// replica poller. The dial fixture uses net.Pipe and makes no host connection.
func TestWaitReadyTCP_EmitsActualProbeCount(t *testing.T) {
	for _, failures := range []int{0, 1, 3, 6} {
		t.Run(strconv.Itoa(failures)+" failed probes", func(t *testing.T) {
			store := state.NewMemStore()
			v := NewJailerVMM(t.TempDir(), time.Second)
			v.events = buildReadinessPlatform(t, store)
			lease := Lease{Instance: "tcp-count", HostIP: netip.MustParseAddr("127.0.0.1")}
			ctx := wire.WithContext(context.Background(), wire.CorrelationFields{
				WakeID: "wake-tcp-count", AppID: "app-tcp-count", NodeID: "node-tcp-count",
			})
			calls := 0
			v.tcpReadinessDial = func(network, addr string, timeout time.Duration) (net.Conn, error) {
				calls++
				if network != "tcp" || addr != "127.0.0.1:8080" || timeout != 200*time.Millisecond {
					t.Fatalf("probe target/timeout changed: %s %s %s", network, addr, timeout)
				}
				if calls <= failures {
					return nil, errors.New("connection refused")
				}
				a, b := net.Pipe()
				t.Cleanup(func() { _ = b.Close() })
				return a, nil
			}
			if err := v.waitReady(ctx, lease, ""); err != nil {
				t.Fatal(err)
			}
			want := failures + 1
			if calls != want {
				t.Fatalf("dial attempts=%d, want %d", calls, want)
			}
			var rows []state.Event
			deadline := time.Now().Add(time.Second)
			for len(rows) == 0 && time.Now().Before(deadline) {
				var err error
				rows, err = store.ListEventsByWakeID(ctx, "wake-tcp-count", time.Time{}, 0)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) == 0 {
					time.Sleep(time.Millisecond)
				}
			}
			if len(rows) != 1 || rows[0].Kind != events.WakeReadiness200 {
				t.Fatalf("expected one readiness success event, got %d rows", len(rows))
			}
			var payload map[string]any
			if err := json.Unmarshal(rows[0].Data, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["probe_count"] != float64(want) {
				t.Fatalf("timeline probe_count=%v, want %d actual attempts", payload["probe_count"], want)
			}
			if payload["wake_id"] != "wake-tcp-count" || payload["node_id"] != "node-tcp-count" || payload["instance_id"] != lease.Instance || payload["healthcheck_path"] != "" {
				t.Fatalf("readiness correlation changed: %#v", payload)
			}
		})
	}
}
