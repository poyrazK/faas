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
	listen       = flag.String("listen", "", "serve live frames to realtimed at <addr>/live and <addr>/<database-id>/live; use -tls-cert and -client-ca on any shared network")
	tlsCert      = flag.String("tls-cert", "", "certificate for -listen")
	tlsKey       = flag.String("tls-key", "", "private key for -tls-cert")
	clientCA     = flag.String("client-ca", "", "with -tls-cert: accept only clients (realtimed) whose certificate this CA signed")
	clientName   = flag.String("client-name", "", "with -client-ca: accept only a client certificate valid for this name (realtimed.faas for the fleet PKI's live client leaf)")
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
	// RLS-mode claims are believed only from a peer whose client
	// certificate -client-ca verified: realtimed.
	mux := realtimedMux(*databaseID, liveserver.WebSocketTrusting(hub, func(r *http.Request) bool { return r.Header.Get("Origin") == "" }, liveserver.VerifiedPeer))
	if *tlsCert == "" {
		if *clientCA != "" || *tlsKey != "" {
			return nil, errors.New("livequeryd: -tls-key and -client-ca need -tls-cert")
		}
		log.Warn("livequeryd: -listen without TLS; anyone who can reach it and holds a seal can subscribe", "addr", *listen)
		return serve(*listen, mux, log)
	}
	conf, err := realtimedTLS(*tlsCert, *tlsKey, *clientCA, *clientName)
	if err != nil {
		return nil, err
	}
	return serveTLS(*listen, mux, conf, log)
}

// realtimedTLS is the listener's TLS: the hub's certificate, and with a
// client CA, client certificates that CA signed. A client name narrows those
// to the one identity allowed to carry live frames and RLS claims: without
// it, any leaf of the fleet CA (meterd's, for one) would pass.
func realtimedTLS(certFile, keyFile, caFile, name string) (*tls.Config, error) {
	if name != "" && caFile == "" {
		return nil, errors.New("livequeryd: -client-name needs -client-ca")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("livequeryd: -tls-cert: %w", err)
	}
	conf := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	if caFile == "" {
		return conf, nil
	}
	pem, err := os.ReadFile(caFile) //nolint:forbidigo // operator-provisioned file named by a flag
	if err != nil {
		return nil, fmt.Errorf("livequeryd: -client-ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("livequeryd: -client-ca holds no certificate")
	}
	conf.ClientCAs, conf.ClientAuth = pool, tls.RequireAndVerifyClientCert
	if name != "" {
		conf.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.VerifiedChains) == 0 || len(cs.PeerCertificates) == 0 {
				return errors.New("livequeryd: client certificate not verified")
			}
			if err := cs.PeerCertificates[0].VerifyHostname(name); err != nil {
				return fmt.Errorf("livequeryd: client certificate is not %s: %w", name, err)
			}
			return nil
		}
	}
	return conf, nil
}

// realtimedMux serves live at /live and at /<database>/live, so a backend's
// live_hub_url may name the database ({database_id}) in its path. Another
// database's path is not found.
func realtimedMux(database string, live http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/live", live)
	mux.HandleFunc("/{database}/live", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("database") != database {
			http.NotFound(w, r)
			return
		}
		live.ServeHTTP(w, r)
	})
	return mux
}
