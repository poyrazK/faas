package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/wire"
)

// relayListenSpec splits relay_listen into a network and address. TCP needs
// mTLS; a Unix socket is for single-box hosts where vmmd is local.
func relayListenSpec(listen string) (network, address string, err error) {
	switch {
	case strings.HasPrefix(listen, "unix://"):
		address = strings.TrimPrefix(listen, "unix://")
		if !strings.HasPrefix(address, "/") {
			return "", "", fmt.Errorf("outboundd: relay_listen unix socket must be an absolute path")
		}
		return "unix", address, nil
	case strings.HasPrefix(listen, "tcp://"):
		address = strings.TrimPrefix(listen, "tcp://")
		if _, _, splitErr := net.SplitHostPort(address); splitErr != nil {
			return "", "", fmt.Errorf("outboundd: relay_listen tcp address: %w", splitErr)
		}
		return "tcp", address, nil
	default:
		return "", "", fmt.Errorf("outboundd: relay_listen must start with unix:// or tcp://")
	}
}

// relayTLSConfig loads the relay server leaf and requires a fleet-CA client
// certificate bound to an active compute node (ADR-056 node identity).
func (c *Config) relayTLSConfig(verifier wire.NodeVerifier) (*tls.Config, error) {
	if c.RelayTLSCertPath == "" || c.RelayTLSKeyPath == "" || c.RelayTLSCAPath == "" {
		return nil, errors.New("outboundd: a tcp relay_listen requires relay_tls_cert_path, relay_tls_key_path, and relay_tls_ca_path")
	}
	cfg, err := wire.LoadServerTLSConfigWithPrefixAndVerifier("relay_tls_", c.RelayTLSCertPath, c.RelayTLSKeyPath, c.RelayTLSCAPath, verifier)
	if err != nil {
		return nil, fmt.Errorf("outboundd: relay TLS: %w", err)
	}
	if cfg == nil || cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		return nil, errors.New("outboundd: relay TLS must require verified client certificates")
	}
	return cfg, nil
}

// startRelay serves the gateway handler on the vmmd relay listener. It
// returns a shutdown function; a nil function means the relay is disabled.
func startRelay(ctx context.Context, cfg *Config, pool *pgxpool.Pool, handler http.Handler, identity outbound.IdentityVerifier, metrics *outbound.Metrics, log *slog.Logger) (func(context.Context), error) {
	if cfg.RelayListen == "" {
		return nil, nil
	}
	verifier, ok := identity.(outbound.RelayIdentityVerifier)
	if identity == nil || !ok {
		return nil, errors.New("outboundd: relay_listen requires workload_identity_jwks_path")
	}
	network, address, err := relayListenSpec(cfg.RelayListen)
	if err != nil {
		return nil, err
	}
	var inner net.Listener
	if network == "unix" {
		if inner, err = listenRelaySocket(address); err != nil {
			return nil, err
		}
	} else {
		nodeVerifier := wire.NewPGNodeVerifier(wire.NewPGNodeLoader(pool), log)
		if _, err := nodeVerifier.Refresh(ctx); err != nil {
			return nil, fmt.Errorf("outboundd: relay node verifier refresh: %w", err)
		}
		go func() {
			ch, subErr := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyComputeNodeChanged}, log)
			if subErr != nil {
				log.Error("outboundd: relay node verifier LISTEN failed", "err", subErr)
				return
			}
			if runErr := nodeVerifier.Run(ctx, ch); runErr != nil && !errors.Is(runErr, context.Canceled) {
				log.Error("outboundd: relay node verifier exited", "err", runErr)
			}
		}()
		tlsCfg, err := cfg.relayTLSConfig(nodeVerifier)
		if err != nil {
			return nil, err
		}
		tcp, err := wire.ListenInherited("tcp", address)
		if err != nil {
			return nil, fmt.Errorf("outboundd: relay listen: %w", err)
		}
		inner = tls.NewListener(tcp, tlsCfg)
	}
	listener, err := outbound.NewRelayListener(inner, verifier, metrics, log)
	if err != nil {
		_ = inner.Close()
		return nil, err
	}
	server, err := outbound.NewRelayServer(handler, verifier, cfg.ReadTimeout, cfg.WriteTimeout, cfg.IdleTimeout, log)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("outboundd: relay server stopped", "err", serveErr)
		}
	}()
	log.Info("outboundd relay listening", "listen", cfg.RelayListen)
	return func(shutdownCtx context.Context) { _ = server.Shutdown(shutdownCtx) }, nil
}

// listenRelaySocket prefers a socket a systemd socket unit holds open across
// restarts, so vmmd's relay connections wait for the replacement.
func listenRelaySocket(address string) (net.Listener, error) {
	if l, ok, err := wire.InheritedListener("unix", address); err != nil {
		return nil, fmt.Errorf("outboundd: relay listen: %w", err)
	} else if ok {
		return l, nil
	}
	if err := os.Remove(address); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("outboundd: remove stale relay socket: %w", err)
	}
	l, err := net.Listen("unix", address)
	if err != nil {
		return nil, fmt.Errorf("outboundd: relay listen: %w", err)
	}
	if err := os.Chmod(address, 0o660); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("outboundd: relay socket mode: %w", err)
	}
	return l, nil
}
