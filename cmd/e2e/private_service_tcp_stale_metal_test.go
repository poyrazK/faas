//go:build metal

// adr: 196 — the private TCP twin of TestServiceCallInsideLeaseAfterParkWakesMetal:
// a connection to a service address that lands inside the proxy's endpoint
// lease after the target was parked must wake the target and echo, not fail.
// vmmd reports the parked instance NotFound before the guest dial, the TCP
// proxy retires it and retries once, and the retry finds nothing routable and
// wakes the target (ADR-576).
package e2e_test

import (
	"testing"
	"time"
)

func TestPrivateServiceTCPInsideLeaseAfterParkWakesMetal(t *testing.T) {
	f := newPrivateTCPImageFixture(t)
	if f == nil {
		return
	}
	if got := f.dial(t, "warm-up"); !f.echoed(got, "warm-up") {
		f.h.DumpLogs(t)
		t.Fatalf("warm-up dial to %s:%d = %+v, want the echoed payload", f.address, privateTCPEchoPort, got)
	}

	const cycles = 3
	informative := 0
	for i := 0; i < cycles; i++ {
		// Let the previous lease expire, then refresh it with a session that
		// reaches the running instance: the proxy now holds that instance.
		time.Sleep(5500 * time.Millisecond)
		refresh := "refresh-" + randHexSuffix()
		if got := f.dial(t, refresh); !f.echoed(got, refresh) {
			t.Fatalf("cycle %d: lease-refreshing dial = %+v", i, got)
		}
		leased := time.Now()
		f.park(t, f.service)
		time.Sleep(time.Second)
		elapsed := time.Since(leased)
		payload := "stale-" + randHexSuffix()
		got := f.dial(t, payload)
		if elapsed > staleLeaseBudget {
			t.Logf("cycle %d: park took too long (%s after the lease refresh); lease expired, cycle not informative (%+v)", i, elapsed.Round(time.Millisecond), got)
			continue
		}
		informative++
		t.Logf("cycle %d: dial %s after the lease refresh, target parked: status=%d reply=%q error=%q", i, elapsed.Round(time.Millisecond), got.status, got.Reply, got.Error)
		if !f.echoed(got, payload) {
			t.Errorf("cycle %d: dial inside the stale lease = %+v, want %q echoed (the parked target must be woken)", i, got, payload)
		}
	}
	if informative == 0 {
		t.Fatalf("no cycle landed inside the %s lease budget; the stale-lease path was not exercised", staleLeaseBudget)
	}
}
