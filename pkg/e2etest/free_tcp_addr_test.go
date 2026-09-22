package e2etest

import (
	"sync"
	"testing"
)

// TestClaimAddr_RejectsASecondClaimant pins the guard behind the
// TestE2E_NormalPath_* flake.
//
// freeTCPAddr binds :0 and closes the listener, which is inherently TOCTOU:
// the port is free at that instant and nothing holds it until the daemon
// execs. Two callers could therefore be handed the same port, and the losing
// daemon died on bind. For gatewayd-internal that meant run() returned, its
// `defer pool.Close()` fired, and every route lookup afterwards failed with
// "closed pool" — surfacing as a 404 routing timeout in a test that looks
// nothing like a port conflict. A real CI failure showed apid's loopback
// target and the gateway's control listener both on 127.0.0.1:32997.
//
// This asserts the DECISION rather than driving freeTCPAddr in a loop. An
// earlier version of this test drew 200 addresses and asserted they were
// distinct; it passed identically with the guard disabled, because the kernel
// cycles the ephemeral range and will not hand the same port to two
// sequential draws on demand. It proved nothing.
func TestClaimAddr_RejectsASecondClaimant(t *testing.T) {
	const addr = "127.0.0.1:32997" // the port from the real CI collision

	if !claimAddr(addr) {
		t.Fatalf("first claim of %s was refused", addr)
	}
	if claimAddr(addr) {
		t.Fatalf("second claim of %s succeeded; two daemons would be configured on one port", addr)
	}
	// Still refused on a third ask: the entry is never released, because a
	// port reused by a later daemon is exactly the bug this prevents.
	if claimAddr(addr) {
		t.Fatalf("third claim of %s succeeded; the registry released an address", addr)
	}
}

// TestClaimAddr_DistinctAddressesAreIndependent guards against the opposite
// failure: a guard that refuses everything would make freeTCPAddr exhaust its
// attempts and fail every e2e run.
func TestClaimAddr_DistinctAddressesAreIndependent(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:41001", "127.0.0.1:41002", "127.0.0.1:41003"} {
		if !claimAddr(addr) {
			t.Errorf("claimAddr(%s) refused a previously unseen address", addr)
		}
	}
}

// TestClaimAddr_ConcurrentClaimantsElectOneWinner pins the mutex. The harness
// reserves addresses for several daemons, and a data race here would hand the
// same port to two of them — the exact failure, reintroduced.
func TestClaimAddr_ConcurrentClaimantsElectOneWinner(t *testing.T) {
	const addr = "127.0.0.1:42424"
	const racers = 32

	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if claimAddr(addr) {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d goroutines won the same address; want exactly 1", wins)
	}
}

// TestFreeTCPAddr_RegistersWhatItReturns pins the wiring: the guard above is
// only useful if freeTCPAddr actually consults it. A returned address must
// already be claimed, so a second caller cannot be given it.
func TestFreeTCPAddr_RegistersWhatItReturns(t *testing.T) {
	addr := freeTCPAddr(t)
	if claimAddr(addr) {
		t.Fatalf("freeTCPAddr returned %s without registering it; a later caller could be handed the same port", addr)
	}
}
