package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// outboundRelayServerName is the SAN on outboundd's relay server leaf
// (pkg/pki "outboundd.faas"); the dial address is a host name or IP.
const outboundRelayServerName = "outboundd.faas"

const outboundRelayDialTimeout = 5 * time.Second

// errOutboundRelayNoClientTLS reports a TCP relay target without vmmd's
// outbound client mTLS material: the expected state until the
// outboundd-client leaf is issued (faas_vmmd_outbound_relay_tls_managed
// off), so vmmd logs it as a warning, not an error.
var errOutboundRelayNoClientTLS = errors.New("requires vmmd outbound client mTLS")

// outboundRelayDialer opens one connection to outboundd's relay listener.
type outboundRelayDialer func(context.Context) (net.Conn, error)

// outboundRelayTarget resolves the ADR-989 relay target. Single-box hosts
// use outboundd's local Unix socket. A split-box TCP target needs vmmd's
// node-identity outboundd-client leaf; plaintext TCP is refused. An empty
// target disables the relay, and guest connections are closed at once.
func outboundRelayTarget(getenv func(string) string) (outboundRelayDialer, error) {
	target := strings.TrimSpace(getenv("FAAS_VMMD_OUTBOUND_RELAY_TARGET"))
	if target == "" {
		return nil, nil
	}
	dialer := &net.Dialer{Timeout: outboundRelayDialTimeout, KeepAlive: 30 * time.Second}
	switch {
	case strings.HasPrefix(target, "unix://"):
		path := strings.TrimPrefix(target, "unix://")
		if !strings.HasPrefix(path, "/") {
			return nil, errors.New("outbound relay: unix target must be an absolute path")
		}
		return func(ctx context.Context) (net.Conn, error) { return dialer.DialContext(ctx, "unix", path) }, nil
	case strings.HasPrefix(target, "tcp://"):
		address := strings.TrimPrefix(target, "tcp://")
		if _, _, err := net.SplitHostPort(address); err != nil {
			return nil, fmt.Errorf("outbound relay: tcp target: %w", err)
		}
		tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("vmmd_outbound_client_",
			getenv("FAAS_VMMD_OUTBOUND_CLIENT_TLS_CERT_PATH"),
			getenv("FAAS_VMMD_OUTBOUND_CLIENT_TLS_KEY_PATH"),
			getenv("FAAS_VMMD_OUTBOUND_CLIENT_TLS_CA_PATH"))
		if err != nil {
			return nil, fmt.Errorf("outbound relay client TLS: %w", err)
		}
		if tlsCfg == nil {
			return nil, fmt.Errorf("outbound relay: tcp target %q %w", target, errOutboundRelayNoClientTLS)
		}
		tlsCfg.ServerName = outboundRelayServerName
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: tlsCfg}
		return func(ctx context.Context) (net.Conn, error) { return tlsDialer.DialContext(ctx, "tcp", address) }, nil
	default:
		return nil, fmt.Errorf("outbound relay: target %q must start with unix:// or tcp://", target)
	}
}

// logOutboundRelayDisabled reports a relay vmmd leaves closed. Missing client
// mTLS is the expected state before the leaf is issued; logged at ERROR it
// was the only error line after every rollout (prod check 2026-11-16).
func logOutboundRelayDisabled(log *slog.Logger, err error) {
	level := slog.LevelError
	if errors.Is(err, errOutboundRelayNoClientTLS) {
		level = slog.LevelWarn
	}
	log.Log(context.Background(), level, "vmmd: outbound relay disabled", "err", err)
}
