//go:build !no_pg

// adr: 570
package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Real daemon handlers, node RPC clients and debugger publishers run in two
// OS processes. Placement, VM forwarding and the telemetry receiver are fixtures.
func TestTrafficFleetDaemonRetryCompletionTelemetry(t *testing.T) {
	f := newFleetDaemonFixture(t)
	first, second := f.process(t, 0), f.process(t, 1)
	app := f.apps[2]
	fleetDaemonCall(t, first, app, "/retry", http.StatusOK)
	fleetDaemonCall(t, second, app, "/retry", http.StatusServiceUnavailable)
	fleetDaemonRetries(t, f, 2, 1)
	// Observe the production five-second publisher tick before canceling its
	// context. Retry accounting above is checked while its window is live.
	for until := time.Now().Add(8 * time.Second); len(f.telemetry.records()) < 2 && time.Now().Before(until); {
		select {
		case <-t.Context().Done():
			t.Fatal("telemetry wait canceled")
		case <-time.After(10 * time.Millisecond):
		}
	}
	rows := f.telemetry.records()
	if len(rows) != 2 {
		t.Fatalf("emitted logical records=%d want=2: %v", len(rows), rows)
	}
	statuses := make(map[int32]int)
	traces := make(map[string]bool)
	for _, row := range rows {
		owner := app.Instances[0]
		if row.HttpStatus == http.StatusOK {
			owner = app.Instances[1]
		}
		statuses[row.HttpStatus]++
		if row.AppId != app.ID || row.AccountId != f.app.AccountID || row.DeploymentId != app.Deployment ||
			row.InstanceId != owner || row.NodeId != f.nodes[0].ID || row.Count != 1 || row.ColdBoot ||
			row.WakeId != "" || row.TraceId == "" || traces[row.TraceId] || !row.UsageOutboxed {
			t.Errorf("emitted completion=%+v want owner=%s and one warm logical request", row, owner)
		}
		traces[row.TraceId] = true
	}
	if statuses[200] != 1 || statuses[503] != 1 {
		t.Errorf("logical statuses=%v", statuses)
	}
	calls := f.vm.calls("/retry")
	if len(calls) != 3 || calls[0] != app.Instances[0] || calls[1] != app.Instances[1] || calls[2] != app.Instances[0] || f.vm.guestCalls("/retry") != 1 {
		t.Fatalf("RPC targets=%v guest requests=%d", calls, f.vm.guestCalls("/retry"))
	}
	f.vm.mu.Lock()
	headers := f.vm.guestHeaders["/retry"]
	f.vm.mu.Unlock()
	if len(headers) != 1 || headers[0].Get(api.InstanceIDHeader) != app.Instances[1] || headers[0].Get(api.NodeIDHeader) != f.nodes[0].ID || headers[0].Get(api.DeploymentIDHeader) != app.Deployment {
		t.Errorf("guest dispatch identity=%v", headers)
	}
	first.stop(t)
	second.stop(t)
	if len(f.telemetry.records()) != 2 {
		t.Error("shutdown emitted an extra logical completion")
	}
	t.Log("observed two logical completions for three RPC attempts")
}
