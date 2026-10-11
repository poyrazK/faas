package main

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"time"
)

// OutboundRelayEndpoint is the app-facing base URL for managed outbound
// integrations (ADR-981). Requests use /i/{integration-id}/{provider path};
// guest-init forwards the connection bytes to the host without reading them.
const OutboundRelayEndpoint = "http://" + outboundRelayListenAddr

const (
	outboundRelayListenAddr = "127.0.0.1:2774"
	// outboundRelayMaxConns matches vmmd's per-instance guest stream cap, so
	// guest-init sheds load before the host listener has to.
	outboundRelayMaxConns = 64
)

// relayHostConn is one host stream. CloseWrite forwards a half-close.
type relayHostConn interface {
	io.ReadWriteCloser
	CloseWrite() error
}

// StampOutboundRelayEnv exposes the relay URL after customer values, like
// the other platform-owned loopback endpoints.
func StampOutboundRelayEnv(env []string) []string {
	return append(env, "GREGALE_OUTBOUND_URL="+OutboundRelayEndpoint)
}

// serveOutboundRelay accepts app connections until ln closes. Each one gets
// its own host stream; past the cap new connections close at once.
func serveOutboundRelay(ln net.Listener, dial func() (relayHostConn, error), log *slog.Logger) {
	sem := make(chan struct{}, outboundRelayMaxConns)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Warn("outbound relay accept failed", "err", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		select {
		case sem <- struct{}{}:
			go func() {
				defer func() { <-sem }()
				relayOutboundConn(conn, dial)
			}()
		default:
			_ = conn.Close()
		}
	}
}

// relayOutboundConn copies both directions and forwards half-closes. A
// failed direction closes both ends so the other copy cannot block forever.
func relayOutboundConn(app net.Conn, dial func() (relayHostConn, error)) {
	defer func() { _ = app.Close() }()
	host, err := dial()
	if err != nil {
		return
	}
	defer func() { _ = host.Close() }()
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		if _, err := io.Copy(host, app); err != nil {
			_ = app.Close()
			_ = host.Close()
			return
		}
		_ = host.CloseWrite()
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		if _, err := io.Copy(app, host); err != nil {
			_ = app.Close()
			_ = host.Close()
			return
		}
		if half, ok := app.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		}
	}()
	<-done
	<-done
}
