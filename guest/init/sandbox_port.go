package main

// Exposed ports (ADR-20261027090013 §11). vmmd reaches a guest port over
// vsock, not the network, so a session in any network mode can expose one
// and no netns rule changes per port. The connection goes to loopback only:
// a port the tenant's server binds to 127.0.0.1, ::1 or every address.

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// portDialTimeout bounds reaching the guest port.
const portDialTimeout = 2 * time.Second

// portConnect splices one TCP connection to the guest port with the
// operation's data frames until the port closes its side.
func portConnect(ctx context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	conn, err := dialLoopback(ctx, req.Port)
	if err != nil {
		return nil, sandboxError(sandboxproto.CodePortClosed, "nothing listens on port %d", req.Port)
	}
	defer func() { _ = conn.Close() }()
	var in atomic.Int64
	go func() {
		n, err := call.ReadUpload(conn, math.MaxInt64)
		in.Store(n)
		if err != nil {
			// The host went away: end the port's side too.
			_ = conn.Close()
			return
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	out, err := io.Copy(call.Data(), conn)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return nil, sandboxError(sandboxproto.CodeInternal, "the port connection failed")
	}
	return sandboxproto.PortResult{BytesIn: in.Load(), BytesOut: out}, nil
}

// dialLoopback reaches port on IPv4 loopback, then IPv6 loopback, where a
// server that binds "localhost" may listen alone.
func dialLoopback(ctx context.Context, port int) (net.Conn, error) {
	d := net.Dialer{Timeout: portDialTimeout}
	var errs []error
	for _, host := range []string{"127.0.0.1", "::1"} {
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}
