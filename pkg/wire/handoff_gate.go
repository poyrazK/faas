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
type readyGate struct {
	net.Listener
	ready     <-chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func newReadyGate(l net.Listener, ready <-chan struct{}) *readyGate {
	return &readyGate{Listener: l, ready: ready, closed: make(chan struct{})}
}

func (g *readyGate) Accept() (net.Conn, error) {
	select {
	case <-g.ready:
	case <-g.closed:
		return nil, net.ErrClosed
	}
	return g.Listener.Accept()
}

func (g *readyGate) Close() error {
	g.closeOnce.Do(func() { close(g.closed) })
	return g.Listener.Close()
}
