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
	// ADR 20261028161502: apid signs the live keys and logins it seals.
	liveSignatureKeys     = flag.String("live-signature-keys", "", "file of Ed25519 public keys (PEM) that verify apid's signatures on live keys and logins; a value whose signature does not verify is refused")
	requireLiveSignatures = flag.Bool("require-live-signatures", false, "also refuse live keys and logins that carry no signature (the contract step, after gregalectl live-query sign-existing); needs -live-signature-keys")
)

// keySource reads the databases' live keys from the control plane; each
// hub watches its own database's (liveserver.WatchKeys).
type keySource struct {
	ring       liveserver.KeySource
	identities []*age.X25519Identity
	// verify checks apid's writer signatures; nil checks none.
	verify *livekeys.Verifier
	pool   *pgxpool.Pool
}

// Close closes the control-plane pool. A nil source has none.
func (k *keySource) Close() {
	if k != nil {
		k.pool.Close()
	}
}

// watch keeps hub's key ring current until ctx ends.
func (k *keySource) watch(ctx context.Context, hub *liveserver.Hub, log *slog.Logger) {
	liveserver.WatchKeys(ctx, hub, k.ring, k.identities, k.verify, *keysRefresh, log)
}

// openKeySource connects to the control plane named by -keys-dsn-file, or
// returns nil without -keys-*.
func openKeySource(ctx context.Context) (*keySource, error) {
	if *keysDSNFile == "" && *keysIdentity == "" {
		return nil, nil
	}
	if *keysDSNFile == "" || *keysIdentity == "" {
		return nil, errors.New("livequeryd: -keys-dsn-file and -keys-identity go together")
	}
	identity, err := secretbox.LoadHostKey(*keysIdentity)
	if err != nil {
		return nil, fmt.Errorf("livequeryd: live key identity: %w", err)
	}
	verify, err := liveSignatureVerifier()
	if err != nil {
		return nil, err
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
	return &keySource{ring: livekeys.New(pool).Ring, identities: []*age.X25519Identity{identity}, verify: verify, pool: pool}, nil
}

// liveSignatureVerifier builds the writer-signature check from
// -live-signature-keys and -require-live-signatures; nil without keys.
func liveSignatureVerifier() (*livekeys.Verifier, error) {
	if *liveSignatureKeys == "" {
		if *requireLiveSignatures {
			return nil, errors.New("livequeryd: -require-live-signatures needs -live-signature-keys")
		}
		return nil, nil
	}
	keys, err := livekeys.LoadVerifyKeys(*liveSignatureKeys)
	if err != nil {
		return nil, fmt.Errorf("livequeryd: -live-signature-keys: %w", err)
	}
	return livekeys.NewVerifier(keys, *requireLiveSignatures), nil
}

// serveRealtimed serves live frames to realtimed. It accepts no browser
// origins: browsers reach live queries through realtimed.
func serveRealtimed(routes *routes, log *slog.Logger) (*http.Server, error) {
	// RLS-mode claims are believed only from a peer whose client
	// certificate -client-ca verified: realtimed.
	noBrowser := func(r *http.Request) bool { return r.Header.Get("Origin") == "" }
	mux := realtimedMux(routes, func(h *liveserver.Hub) http.Handler {
		return liveserver.WebSocketTrusting(h, noBrowser, liveserver.VerifiedPeer)
	})
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

// realtimedMux serves each database's live frames at /<database>/live, the
// path a backend's live_hub_url names with {database_id}. A process serving
// one database also answers at /live. A database it does not serve is not
// found; one whose hub is starting answers 503 and realtimed retries.
func realtimedMux(routes *routes, live func(*liveserver.Hub) http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		id, ok := routes.only()
		if !ok {
			http.NotFound(w, r)
			return
		}
		routes.serve(w, r, id, live)
	})
	mux.HandleFunc("/{database}/live", func(w http.ResponseWriter, r *http.Request) {
		routes.serve(w, r, r.PathValue("database"), live)
	})
	return mux
}
