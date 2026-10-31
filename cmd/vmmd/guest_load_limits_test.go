package main

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A guest writes its memory-pressure frames, so vmmd bounds the relays they
// start: one per instance per interval, and a fixed number in flight to
// schedd across the node (ADR-1155).
func TestMemoryPressureLimiterBoundsGuestRelays(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newMemoryPressureLimiter(time.Minute, 2)
	if !l.admit("i-1", now) {
		t.Fatal("first report refused")
	}
	for range 1000 { // a guest looping on the frame
		if l.admit("i-1", now.Add(time.Second)) {
			t.Fatal("repeat report within the interval admitted")
		}
	}
	if !l.admit("i-2", now) {
		t.Fatal("another instance's report refused")
	}
	if l.admit("i-3", now) {
		t.Fatal("report admitted past the in-flight cap")
	}
	l.done()
	if !l.admit("i-3", now) {
		t.Fatal("report refused after a relay finished")
	}
	l.done()
	l.done()
	if !l.admit("i-1", now.Add(time.Minute)) {
		t.Fatal("report refused after the interval")
	}
	if api.ProfileMemoryPressureRelayMinInterval < api.ProfileMemoryPressurePollInterval || api.ProfileMemoryPressureRelayInFlight < 1 {
		t.Fatal("relay bounds out of range")
	}
}

// Every runtime-config request reads the database and may open sealed
// secrets, so each instance gets a request rate; one instance's loop never
// spends another's allowance.
func TestRuntimeConfigLimiterHoldsEachInstanceToItsRate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var l runtimeConfigLimiter
	admitted := 0
	for range 1000 {
		if l.allow("noisy", now) {
			admitted++
		}
	}
	if admitted != api.RuntimeConfigRequestBurst {
		t.Fatalf("a burst admitted %d requests, want %d", admitted, api.RuntimeConfigRequestBurst)
	}
	if !l.allow("quiet", now) {
		t.Fatal("another instance's request refused")
	}
	if !l.allow("noisy", now.Add(time.Second)) {
		t.Fatal("the bucket did not refill")
	}
	// guest-init's own polling (secrets every 10 s, dev patches every second)
	// stays far inside the rate.
	if api.RuntimeConfigRequestsPerSecond < 5 || api.RuntimeConfigRequestBurst < api.RuntimeConfigRequestsPerSecond {
		t.Fatal("runtime-config rate below guest-init's own polling")
	}
}
