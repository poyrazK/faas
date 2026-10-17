package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

var (
	listen       = flag.String("listen", "", "serve live frames to realtimed at <addr>/live; use -tls-cert and -client-ca on any shared network")
	tlsCert      = flag.String("tls-cert", "", "certificate for -listen")
	tlsKey       = flag.String("tls-key", "", "private key for -tls-cert")
	clientCA     = flag.String("client-ca", "", "with -tls-cert: accept only clients (realtimed) whose certificate this CA signed")
	keysDSNFile  = flag.String("keys-dsn-file", "", "file holding a read-only control-plane connection string; the database's live keys are read there")
	keysIdentity = flag.String("keys-identity", "", "age identity that opens the live keys apid sealed (the pair of apid's FAAS_APID_LIVE_KEY_RECIPIENT_FILE)")
	keysRefresh  = flag.Duration("keys-refresh", 0, "how often to read the live keys (default 5s); revocations apply within it")
)

// watchKeys starts reading the database's live keys from the control plane.
// The returned function closes the control-plane pool.
func watchKeys(ctx context.Context, hub *liveserver.Hub, log *slog.Logger) (func(), error) {
	if *keysDSNFile == "" || *keysIdentity == "" {
		return nil, errors.New("livequeryd: -keys-dsn-file and -keys-identity go together")
	}
	identity, err := secretbox.LoadHostKey(*keysIdentity)
	if err != nil {
		return nil, fmt.Errorf("livequeryd: live key identity: %w", err)
	}
	data, err := os.ReadFile(*keysDSNFile) //nolint:forbidigo // operator-provisioned file named by a flag
	if err != nil {
		return nil, fmt.Errorf("livequeryd: read -keys-dsn-file: %w", err)
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("livequeryd: control-plane connection string: %w", err)
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["application_name"] = "faas-livequeryd"
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("livequeryd: control-plane pool: %w", err)
	}
	go liveserver.WatchKeys(ctx, hub, livekeys.New(pool).Ring, []*age.X25519Identity{identity}, *keysRefresh, log)
	return pool.Close, nil
}

// serveRealtimed serves live frames to realtimed. It accepts no browser
// origins: browsers reach live queries through realtimed.
func serveRealtimed(hub *liveserver.Hub, log *slog.Logger) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.Handle("/live", liveserver.WebSocket(hub, func(r *http.Request) bool { return r.Header.Get("Origin") == "" }))
	if *tlsCert == "" {
		if *clientCA != "" || *tlsKey != "" {
			return nil, errors.New("livequeryd: -tls-key and -client-ca need -tls-cert")
		}
		log.Warn("livequeryd: -listen without TLS; anyone who can reach it and holds a seal can subscribe", "addr", *listen)
		return serve(*listen, mux, log)
	}
	cert, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
	if err != nil {
		return nil, fmt.Errorf("livequeryd: -tls-cert: %w", err)
	}
	conf := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	if *clientCA != "" {
		pem, err := os.ReadFile(*clientCA) //nolint:forbidigo // operator-provisioned file named by a flag
		if err != nil {
			return nil, fmt.Errorf("livequeryd: -client-ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("livequeryd: -client-ca holds no certificate")
		}
		conf.ClientCAs, conf.ClientAuth = pool, tls.RequireAndVerifyClientCert
	}
	return serveTLS(*listen, mux, conf, log)
}
