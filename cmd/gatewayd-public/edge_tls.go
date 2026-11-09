package main

// adr: 20261102081618, 20261103163954 — a compute edge's public TLS listener.
//
// Behind Cloudflare (ADR-20261102081618), Cloudflare connects straight to
// every compute node's public address instead of through a tunnel. The
// listener presents the node's Origin CA certificate and requires the zone's
// Authenticated Origin Pulls client certificate, so only our Cloudflare zone
// gets a request through. Requests on it are trusted ingress: the client
// address comes from CF-Connecting-IP, as it does for the tunnel connector on
// loopback.
//
// With no front (FAAS_EDGE_PUBLIC_FRONT=none, ADR-20261103163954 §2) clients
// connect directly. The listener presents a publicly trusted certificate,
// completes a handshake only for an app host's server name, and offers
// HTTP/2 with explicit limits. It still verifies the zone's client
// certificate when one is given, so a proxied record keeps working: that is
// how a host goes back behind Cloudflare under attack. Only such a verified
// connection is trusted ingress; on any other the client address is the
// peer's and a client's CF-Connecting-IP or X-Forwarded-For is ignored.
//
// The certificate comes from files, or (FAAS_EDGE_PUBLIC_TLS_CERT_SOURCE=
// database, ADR-20261103163954 §3) from the apps domains' certificates
// edgednsd stores: the edge opens their keys with the edge-tls seal domain's
// identity and holds them in memory only.
//
// Off unless FAAS_EDGE_PUBLIC_TLS_ADDR is set.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgecert"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	// edgeTLSAddrEnv is the listener's address, e.g. ":443".
	edgeTLSAddrEnv = "FAAS_EDGE_PUBLIC_TLS_ADDR"
	// edgeTLSCertEnv and edgeTLSKeyEnv are the node's Origin CA
	// certificate and key, PEM.
	edgeTLSCertEnv = "FAAS_EDGE_PUBLIC_TLS_CERT_FILE"
	edgeTLSKeyEnv  = "FAAS_EDGE_PUBLIC_TLS_KEY_FILE"
	// edgeTLSClientCAEnv is the CA that signed the zone's Authenticated
	// Origin Pulls certificate, PEM.
	edgeTLSClientCAEnv = "FAAS_EDGE_PUBLIC_TLS_CLIENT_CA_FILE"
	// edgeTLSFrontEnv names what stands in front of the listener:
	// "cloudflare" (the default) or "none".
	edgeTLSFrontEnv = "FAAS_EDGE_PUBLIC_FRONT"
	// edgeTLSCertSourceEnv names where the certificate comes from: "file"
	// (the default, the two files above) or "database".
	edgeTLSCertSourceEnv = "FAAS_EDGE_PUBLIC_TLS_CERT_SOURCE"
	// edgeTLSCertCAEnv is the CA whose certificates a database source
	// serves: letsencrypt (the default) or letsencrypt-staging.
	edgeTLSCertCAEnv = "FAAS_EDGE_PUBLIC_TLS_CERT_CA"
	// edgeTLSIdentityEnv is the edge-tls seal domain's identity, which the
	// seal_domains role stages for gatewayd-public on compute nodes.
	edgeTLSIdentityEnv = "FAAS_SEAL_DOMAIN_EDGE_TLS_IDENTITY_PATH"
)

// The certificate sources.
const (
	edgeCertFromFile     = "file"
	edgeCertFromDatabase = "database"
)

// edgeFront is what stands in front of the edge's TLS listener.
type edgeFront string

const (
	// edgeFrontCloudflare: Cloudflare's proxy, proven by the zone's client
	// certificate (ADR-20261102081618).
	edgeFrontCloudflare edgeFront = "cloudflare"
	// edgeFrontNone: clients connect directly (ADR-20261103163954).
	edgeFrontNone edgeFront = "none"
)

// edgeTLSConfig is the compute edge's public TLS listener. The zero value is
// off.
type edgeTLSConfig struct {
	addr, certFile, keyFile, clientCAFile string
	front                                 edgeFront
	// source is edgeCertFromFile or edgeCertFromDatabase; ca and
	// identityPath apply to the database.
	source, ca, identityPath string
}

// edgeTLSConfigFromEnv reads the listener's settings. An address needs the
// client CA and a certificate, from both files or the database, on either
// front, and settings without an address are a mistake too: either way the
// edge would not serve what the operator meant. Only a compute edge takes
// them.
func edgeTLSConfigFromEnv(mode publicEdgeMode, getenv func(string) string) (edgeTLSConfig, error) {
	c := edgeTLSConfig{
		addr:         strings.TrimSpace(getenv(edgeTLSAddrEnv)),
		certFile:     strings.TrimSpace(getenv(edgeTLSCertEnv)),
		keyFile:      strings.TrimSpace(getenv(edgeTLSKeyEnv)),
		clientCAFile: strings.TrimSpace(getenv(edgeTLSClientCAEnv)),
		source:       strings.TrimSpace(getenv(edgeTLSCertSourceEnv)),
		ca:           strings.TrimSpace(getenv(edgeTLSCertCAEnv)),
		// Staged on every compute node once the domain is, whether or not
		// the listener uses it, so it alone does not turn the listener on.
		identityPath: strings.TrimSpace(getenv(edgeTLSIdentityEnv)),
	}
	switch front := edgeFront(strings.TrimSpace(getenv(edgeTLSFrontEnv))); front {
	case "":
	case edgeFrontCloudflare, edgeFrontNone:
		c.front = front
	default:
		return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: %s=%q, want %s or %s", edgeTLSFrontEnv, front, edgeFrontCloudflare, edgeFrontNone)
	}
	// Naming the default front alone leaves the listener off.
	if c == (edgeTLSConfig{front: c.front, identityPath: c.identityPath}) && c.front != edgeFrontNone {
		return edgeTLSConfig{}, nil
	}
	if mode != edgeModeCompute {
		return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: %s and its files are for a compute edge (%s=%s)", edgeTLSAddrEnv, publicEdgeModeEnv, edgeModeCompute)
	}
	required := []struct{ env, value string }{{edgeTLSAddrEnv, c.addr}, {edgeTLSClientCAEnv, c.clientCAFile}}
	switch c.source {
	case "", edgeCertFromFile:
		c.source = edgeCertFromFile
		if c.ca != "" {
			return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: %s is for %s=%s", edgeTLSCertCAEnv, edgeTLSCertSourceEnv, edgeCertFromDatabase)
		}
		required = append(required, struct{ env, value string }{edgeTLSCertEnv, c.certFile}, struct{ env, value string }{edgeTLSKeyEnv, c.keyFile})
	case edgeCertFromDatabase:
		if c.certFile != "" || c.keyFile != "" {
			return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: %s=%s takes no %s or %s", edgeTLSCertSourceEnv, edgeCertFromDatabase, edgeTLSCertEnv, edgeTLSKeyEnv)
		}
		switch c.ca {
		case "":
			c.ca = edgecert.CALetsEncrypt
		case edgecert.CALetsEncrypt, edgecert.CALetsEncryptStaging:
		default:
			return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: %s=%q, want %s or %s", edgeTLSCertCAEnv, c.ca, edgecert.CALetsEncrypt, edgecert.CALetsEncryptStaging)
		}
		required = append(required, struct{ env, value string }{edgeTLSIdentityEnv, c.identityPath})
	default:
		return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: %s=%q, want %s or %s", edgeTLSCertSourceEnv, c.source, edgeCertFromFile, edgeCertFromDatabase)
	}
	var missing []string
	for _, setting := range required {
		if setting.value == "" {
			missing = append(missing, setting.env)
		}
	}
	if len(missing) > 0 {
		return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: the edge TLS listener needs %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// direct reports whether clients reach the listener with no proxy in front.
func (c edgeTLSConfig) direct() bool { return c.front == edgeFrontNone }

// fromDatabase reports whether the certificate comes from the apps domains'
// certificates edgednsd stores.
func (c edgeTLSConfig) fromDatabase() bool { return c.source == edgeCertFromDatabase }

func (c edgeTLSConfig) enabled() bool { return c.addr != "" }

// errNotAnAppHost fails a handshake whose server name is missing or no app
// host's, before any HTTP work.
var errNotAnAppHost = errors.New("gatewayd-public: edge TLS server name is not an app host")

// serverConfig loads the client CA and the certificate, from its files or,
// for the database source, through served. Behind Cloudflare, a connection
// whose certificate does not chain to that CA fails its handshake, before
// any request is read. With no front the certificate is verified only when
// given, the server name must be one appHost accepts, and the client may
// speak HTTP/2.
func (c edgeTLSConfig) serverConfig(appHost func(name string) bool, served *edgecert.Served) (*tls.Config, error) {
	var certs []tls.Certificate
	var getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	if c.fromDatabase() {
		if served == nil {
			return nil, errors.New("gatewayd-public: the edge TLS certificate comes from the database, but none is loaded")
		}
		getCertificate = served.GetCertificate
	} else {
		cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
		if err != nil {
			return nil, fmt.Errorf("gatewayd-public: edge TLS certificate: %w", err)
		}
		certs = []tls.Certificate{cert}
	}
	caPEM, err := os.ReadFile(c.clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: edge TLS client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("gatewayd-public: edge TLS client CA %s holds no PEM certificate", c.clientCAFile)
	}
	if err := refuseSharedOriginPullCA(caPEM); err != nil {
		return nil, fmt.Errorf("gatewayd-public: edge TLS client CA %s: %w", c.clientCAFile, err)
	}
	if c.direct() {
		return &tls.Config{
			MinVersion:     tls.VersionTLS12,
			Certificates:   certs,
			GetCertificate: getCertificate,
			ClientAuth:     tls.VerifyClientCertIfGiven,
			ClientCAs:      clientCAs,
			NextProtos:     []string{"h2", "http/1.1"},
			// Scans of the node's address name no app host, or none at
			// all, and get no certificate.
			GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				if hello.ServerName == "" || !appHost(hello.ServerName) {
					return nil, errNotAnAppHost
				}
				return nil, nil
			},
		}, nil
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		Certificates:   certs,
		GetCertificate: getCertificate,
		ClientAuth:     tls.RequireAndVerifyClientCert,
		ClientCAs:      clientCAs,
		// Cloudflare speaks HTTP/1.1 to an origin unless the zone turns on
		// HTTP/2 to origin; this listener offers HTTP/1.1 only.
		NextProtos: []string{"http/1.1"},
	}, nil
}

// sharedOriginPullName names Cloudflare's shared origin-pull certificate and
// its CA. Cloudflare presents that certificate for every customer's zone.
const sharedOriginPullName = "origin-pull.cloudflare.net"

// refuseSharedOriginPullCA fails when caPEM holds Cloudflare's shared
// origin-pull CA. Trusting it lets any Cloudflare zone pass as ours, which
// the client certificate exists to stop: the connection would be trusted
// ingress (CF-Connecting-IP) and exempt from the per-address limits
// (ADR-20261103172551, ADR-20261107180000). Cloudflare's documentation ships
// that CA as origin-pull-ca.pem, the same file name as the zone's own.
func refuseSharedOriginPullCA(caPEM []byte) error {
	rest := caPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("parse a certificate: %w", err)
		}
		for _, name := range []string{cert.Subject.CommonName, cert.Issuer.CommonName} {
			if strings.EqualFold(strings.TrimSpace(name), sharedOriginPullName) {
				return fmt.Errorf("it is Cloudflare's shared origin-pull CA (%s), which every Cloudflare zone satisfies; install the zone's own origin-pull CA (docs/runbooks/EdgeDirectEntry.md)", sharedOriginPullName)
			}
		}
	}
}

// openEdgeTLSListener claims the listener's TCP socket, which a hand-off
// passes on like the others, and serves TLS on it, within the per-address
// limits when they apply. appHost names the server names a listener with no
// front completes a handshake for; served holds the database source's
// certificates; failed counts a handshake the limits' gate saw fail.
// Handshakes in the gate end with ctx.
func openEdgeTLSListener(ctx context.Context, c edgeTLSConfig, appHost func(name string) bool, served *edgecert.Served, limits edgeClientLimits, failed func(net.Addr, error)) (net.Listener, error) {
	cfg, err := c.serverConfig(appHost, served)
	if err != nil {
		return nil, err
	}
	ln, err := wire.ListenInherited("tcp", c.addr)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: listen edge TLS %s: %w", c.addr, err)
	}
	return limits.tlsListener(ctx, ln, cfg, c.direct(), failed), nil
}

// attach makes srv trust the TLS listener's connections and count its failed
// handshakes. With no front it lets srv serve HTTP/2 over TLS, with explicit
// limits rather than library defaults; srv.MaxHeaderBytes caps an HTTP/2
// header list as it does an HTTP/1.1 header. It returns the handshake counter,
// nil while the listener is off, when srv keeps its defaults.
func (c edgeTLSConfig) attach(srv *http.Server, log *slog.Logger, failures prometheus.Counter) *edgeServerErrors {
	if !c.enabled() {
		return nil
	}
	errs := newEdgeServerErrors(log, failures)
	srv.ConnContext = markVerifiedIngress
	srv.ErrorLog = stdlog.New(errs, "", 0)
	if c.direct() {
		if srv.Protocols != nil {
			srv.Protocols.SetHTTP2(true)
		}
		srv.HTTP2 = &http.HTTP2Config{
			MaxConcurrentStreams:      api.EdgeHTTP2MaxConcurrentStreams,
			MaxReadFrameSize:          api.EdgeHTTP2MaxReadFrameSize,
			MaxDecoderHeaderTableSize: api.EdgeHTTP2MaxHeaderTableSize,
			MaxEncoderHeaderTableSize: api.EdgeHTTP2MaxHeaderTableSize,
		}
	}
	return errs
}

// markVerifiedIngress is the public server's ConnContext. Only the TLS
// listener yields *tls.Conn. It verifies a client certificate before a
// request is read; the gateway checks that certificate again per request, so
// a direct client's connection, which has none, is marked but trusts nothing.
func markVerifiedIngress(ctx context.Context, c net.Conn) context.Context {
	if _, ok := c.(*tls.Conn); ok {
		return gateway.WithVerifiedIngress(ctx)
	}
	return ctx
}

// edgeServerErrors is the public server's ErrorLog while the TLS listener is
// on. net/http reports a failed handshake only through that log. Each one
// counts, and at most one a minute is logged: a zone that stopped sending its
// client certificate fails every connection.
type edgeServerErrors struct {
	log      *slog.Logger
	failures prometheus.Counter
	now      func() time.Time
	lastLog  atomic.Int64
}

func newEdgeServerErrors(log *slog.Logger, failures prometheus.Counter) *edgeServerErrors {
	return &edgeServerErrors{log: log, failures: failures, now: time.Now}
}

func (e *edgeServerErrors) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if !strings.Contains(msg, "TLS handshake error") {
		e.log.Warn("gatewayd-public: public server", "err", msg)
		return len(p), nil
	}
	e.handshake(msg)
	return len(p), nil
}

// handshakeFailed counts a handshake the client limits' gate saw fail, before
// the server had the connection, the way net/http reports its own.
func (e *edgeServerErrors) handshakeFailed(remote net.Addr, err error) {
	e.handshake(fmt.Sprintf("http: TLS handshake error from %s: %v", remote, err))
}

func (e *edgeServerErrors) handshake(msg string) {
	e.failures.Inc()
	now := e.now().UnixNano()
	last := e.lastLog.Load()
	if now-last >= int64(time.Minute) && e.lastLog.CompareAndSwap(last, now) {
		e.log.Warn("gatewayd-public: edge TLS handshake failed", "err", msg)
	}
}

// startEdgeCertificates loads the database source's certificates and keeps
// them current (ADR-20261103163954 §3). The edge is not ready until one is
// loaded, so edgednsd sends no client to a node that cannot complete its
// handshake; once loaded, a lost Postgres or a bad row keeps the ones it
// has. It returns nil for the file source.
func startEdgeCertificates(ctx context.Context, c edgeTLSConfig, pool *pgxpool.Pool, probe *gateway.ReadyzProbe, reg prometheus.Registerer, log *slog.Logger) (*edgecert.Served, error) {
	if !c.enabled() || !c.fromDatabase() {
		return nil, nil
	}
	if pool == nil {
		return nil, errors.New("gatewayd-public: the edge TLS certificate comes from the database, and there is none")
	}
	identities, err := sealdomain.LoadReaderIdentities(c.identityPath)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: the edge-tls identity: %w", err)
	}
	sealdomain.RegisterLoaded(reg, sealdomain.EdgeTLS, true)
	served, err := edgecert.NewServed(c.ca, identities, edgecert.NewPgStore(pool), log)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: %w", err)
	}
	reg.MustRegister(served.Collectors()...)
	signal := probe.Register()
	ready := func(loaded bool) {
		if loaded {
			signal.Set(true, "")
			return
		}
		signal.Set(false, "no edge TLS certificate loaded")
	}
	if _, err := served.Load(ctx); err != nil {
		log.Warn("gatewayd-public: no edge TLS certificate yet; not ready until one loads", "ca", c.ca, "err", err)
	}
	ready(served.Loaded())
	go served.Watch(ctx, pool, ready)
	return served, nil
}
