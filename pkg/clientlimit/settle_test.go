package clientlimit

import (
	"net"
	"net/netip"
	"testing"
	"time"
)

// ADR-20261104221012: an address in an exempt proxy range is only
// provisionally free. Settle keeps a vouched connection untracked and counts
// an unvouched one against its address, refusing it past the limit.
func TestListenerSettleCountsUnvouchedProxyConnections(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused refusals
	ln := NewListener(inner, ConnLimits{MaxOpenPerAddr: 1, NewPerSecond: 100, NewBurst: 100, TrackedAddrsMax: 10,
		Exempt: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}, refused.add, nil)
	conns := acceptAll(t, ln)
	next := func() net.Conn {
		t.Helper()
		c, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		select {
		case s := <-conns:
			t.Cleanup(func() { _ = s.Close() })
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("a connection from a proxy address was not accepted")
			return nil
		}
	}

	// Vouched connections stay exempt, however many.
	for range 3 {
		if reason, ok := ln.Settle(next(), true); !ok {
			t.Fatalf("vouched connection refused: %s", reason)
		}
	}
	if got := len(ln.table.entries); got != 0 {
		t.Fatalf("vouched proxy connections tracked: %d entries", got)
	}

	// An unvouched one takes the address's single slot ...
	first := next()
	if reason, ok := ln.Settle(first, false); !ok {
		t.Fatalf("first unvouched connection refused: %s", reason)
	}
	// ... so the next is refused for open connections.
	if reason, ok := ln.Settle(next(), false); ok || reason != ReasonOpenConnections {
		t.Fatalf("second unvouched connection: ok=%v reason=%q, want refused for %s", ok, reason, ReasonOpenConnections)
	}
	// Closing the first gives its slot back.
	_ = first.Close()
	if reason, ok := ln.Settle(next(), false); !ok {
		t.Fatalf("unvouched connection after a close refused: %s", reason)
	}
	// Settling twice changes nothing.
	c := next()
	if _, ok := ln.Settle(c, true); !ok {
		t.Fatal("vouched connection refused")
	}
	if _, ok := ln.Settle(c, false); !ok {
		t.Fatal("a settled connection was settled again")
	}
}

// A connection Accept already limited passes Settle unchanged.
func TestListenerSettleIgnoresLimitedConnections(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused refusals
	ln := NewListener(inner, ConnLimits{MaxOpenPerAddr: 1, NewPerSecond: 100, NewBurst: 100, TrackedAddrsMax: 10}, refused.add, nil)
	conns := acceptAll(t, ln)
	c, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	s := <-conns
	defer func() { _ = s.Close() }()
	if reason, ok := ln.Settle(s, false); !ok {
		t.Fatalf("an already limited connection was refused: %s", reason)
	}
}
