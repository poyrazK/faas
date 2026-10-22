package wire

import (
	"net"
	"sync"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// handoffReady is daemonunit.HandoffReady; tests substitute their own.
var handoffReady = daemonunit.HandoffReady

// readyGate holds Accept until ready closes. A hand-off successor shares
// its listening sockets with a predecessor that is still serving; while the
// successor starts, the predecessor accepts every connection, so none waits
// on a process that cannot answer it yet (ADR-1736).
//
// A checking predecessor's connections for this listener come first: Accept
// returns them before ready, so the checks run through the server that will
// serve traffic while no one else reaches it (handoff_probe.go).
type readyGate struct {
	net.Listener
	ready     <-chan struct{}
	probes    chan net.Conn // filled and closed at construction
	closed    chan struct{}
	closeOnce sync.Once
}

func newReadyGate(l net.Listener, ready <-chan struct{}, probes ...net.Conn) *readyGate {
	g := &readyGate{Listener: l, ready: ready, probes: make(chan net.Conn, len(probes)), closed: make(chan struct{})}
	for _, c := range probes {
		g.probes <- c
	}
	close(g.probes)
	return g
}

func (g *readyGate) Accept() (net.Conn, error) {
	if c, ok := <-g.probes; ok {
		return c, nil
	}
	select {
	case <-g.ready:
	case <-g.closed:
		return nil, net.ErrClosed
	}
	return g.Listener.Accept()
}

func (g *readyGate) Close() error {
	g.closeOnce.Do(func() {
		close(g.closed)
		for c := range g.probes {
			_ = c.Close()
		}
	})
	return g.Listener.Close()
}
