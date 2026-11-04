package managedrealtime

import (
	"testing"
	"time"
)

// adr: 1323 (amendment of 2026-11-03) — the endpoint reconcile skips a node
// that already holds the same registration: listed in its last inventory and
// sent with the same fingerprint recently. A node that lost its endpoints,
// a changed registration or an old send is sent again.
func TestEndpointRegistrationsSkipUnchanged(t *testing.T) {
	var r endpointRegistrations
	now := time.Now()
	if r.unchanged("node", "ep", "f1", now) {
		t.Fatal("a registration never sent was skipped")
	}
	r.recordInventory("node", []string{"ep"})
	r.recordSent("node", "ep", "f1", now)
	if !r.unchanged("node", "ep", "f1", now) {
		t.Fatal("an unchanged registration the node holds was sent again")
	}
	if r.unchanged("node", "ep", "f2", now) {
		t.Fatal("a changed registration was skipped")
	}
	if r.unchanged("node", "ep", "f1", now.Add(endpointRegistrationRefresh)) {
		t.Fatal("a registration past its refresh was skipped")
	}
	r.recordInventory("node", nil) // the node restarted empty
	if r.unchanged("node", "ep", "f1", now) {
		t.Fatal("a node that lost the endpoint was skipped")
	}
	r.recordInventory("node", []string{"ep"})
	r.forget("ep")
	if r.unchanged("node", "ep", "f1", now) {
		t.Fatal("a removed endpoint stayed registered")
	}
}
