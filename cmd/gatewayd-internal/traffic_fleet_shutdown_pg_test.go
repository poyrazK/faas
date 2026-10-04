//go:build !no_pg

// adr: 531
package main

import (
	"net/http"
	"testing"
)

// Stop each real daemon before its ordinary publisher tick. The fixture
// receiver captures RPC records; VM forwarding and placement remain fixtures.
func TestTrafficFleetDaemonShutdownPublishesPendingTelemetry(t *testing.T) {
	f := newFleetDaemonFixture(t)
	for node := range 2 {
		process := f.process(t, node)
		before := len(f.telemetry.records())
		fleetDaemonCall(t, process, f.apps[0], "/work", http.StatusOK)
		if len(f.telemetry.records()) != before {
			t.Fatal("ordinary tick ran before shutdown; pending flush was not exercised")
		}
		process.stop(t)
		rows := f.telemetry.records()
		if len(rows) != node+1 {
			t.Errorf("after daemon %d shutdown: records=%d want=%d", node, len(rows), node+1)
		}
	}
	traces := make(map[string]bool)
	for _, row := range f.telemetry.records() {
		if row.Count != 1 || row.AppId != f.apps[0].ID || row.AccountId != f.app.AccountID || row.DeploymentId != f.apps[0].Deployment ||
			row.InstanceId != f.apps[0].Instances[0] || row.HttpStatus != http.StatusOK || row.TraceId == "" || traces[row.TraceId] || !row.UsageOutboxed {
			t.Errorf("shutdown completion=%+v", row)
		}
		traces[row.TraceId] = true
	}
	if len(f.vm.calls("/work")) != 2 || f.vm.guestCalls("/work") != 2 {
		t.Fatal("shutdown fixture did not serve two real guest requests")
	}
}
