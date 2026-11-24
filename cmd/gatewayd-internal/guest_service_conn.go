// adr: 384 amendment 1 — guest connections to the service proxy carry no TCP
// keepalive and close after GatewaydInternalGuestServiceIdleTimeoutSecondsDefault
// idle.
package main

import (
	"net"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// guestServiceIdleTimeout bounds an idle guest connection to the service
// proxy. Every close of an idle connection, and the reconnect a gRPC client
// makes right after it, resumes a quiet-paused caller.
const guestServiceIdleTimeout = time.Duration(api.GatewaydInternalGuestServiceIdleTimeoutSecondsDefault) * time.Second

// guestServiceListener turns TCP keepalive off on every connection a guest
// opens to the service proxy. Go enables it on accepted connections by
// default (a probe every 15 s once idle, inherited listeners included), and
// each probe is a packet for the guest: it resumes a quiet-paused caller
// that holds idle service connections. The HTTP server's idle timeout
// already closes idle connections, and its read and write timeouts bound one
// in use, so the probes find nothing they do not.
type guestServiceListener struct{ net.Listener }

func (l guestServiceListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(false)
	}
	return conn, nil
}

// guestServiceServer applies the guest connection lifetime to a service
// proxy server.
func guestServiceServer(srv *http.Server) {
	srv.IdleTimeout = guestServiceIdleTimeout
}
