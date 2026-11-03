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
// With no front (FAAS_EDGE_PUBLIC_FRONT=none, ADR-20261103163954) clients
// connect directly. The listener presents a publicly trusted certificate,
// asks for no client certificate and offers HTTP/2. Nothing on such a
// connection is trusted ingress, so the client address is the peer's and a
// client's CF-Connecting-IP or X-Forwarded-For is ignored.
//
// Off unless FAAS_EDGE_PUBLIC_TLS_ADDR is set.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/gateway"
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
}

// edgeTLSConfigFromEnv reads the listener's settings. An address needs its
// certificate and key, plus the client CA behind Cloudflare; settings without
// an address are a mistake too: either way the edge would not serve what the
// operator meant. A client CA with no front is refused, because it would turn
// away every browser. Only a compute edge takes them.
func edgeTLSConfigFromEnv(mode publicEdgeMode, getenv func(string) string) (edgeTLSConfig, error) {
	c := edgeTLSConfig{
		addr:         strings.TrimSpace(getenv(edgeTLSAddrEnv)),
		certFile:     strings.TrimSpace(getenv(edgeTLSCertEnv)),
		keyFile:      strings.TrimSpace(getenv(edgeTLSKeyEnv)),
		clientCAFile: strings.TrimSpace(getenv(edgeTLSClientCAEnv)),
	}
	switch front := edgeFront(strings.TrimSpace(getenv(edgeTLSFrontEnv))); front {
	case "":
	case edgeFrontCloudflare, edgeFrontNone:
		c.front = front
	default:
		return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: %s=%q, want %s or %s", edgeTLSFrontEnv, front, edgeFrontCloudflare, edgeFrontNone)
	}
	// Naming the default front alone leaves the listener off.
	if c == (edgeTLSConfig{front: c.front}) && c.front != edgeFrontNone {
		return edgeTLSConfig{}, nil
	}
	if mode != edgeModeCompute {
		return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: %s and its files are for a compute edge (%s=%s)", edgeTLSAddrEnv, publicEdgeModeEnv, edgeModeCompute)
	}
	required := []struct{ env, value string }{
		{edgeTLSAddrEnv, c.addr},
		{edgeTLSCertEnv, c.certFile},
		{edgeTLSKeyEnv, c.keyFile},
	}
	if c.front == edgeFrontNone {
		if c.clientCAFile != "" {
			return edgeTLSConfig{}, fmt.Errorf("gatewayd-public: %s is for a listener behind Cloudflare; with %s=%s it would refuse every client", edgeTLSClientCAEnv, edgeTLSFrontEnv, edgeFrontNone)
		}
	} else {
		required = append(required, struct{ env, value string }{edgeTLSClientCAEnv, c.clientCAFile})
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

func (c edgeTLSConfig) enabled() bool { return c.addr != "" }

// serverConfig loads the certificate and, behind Cloudflare, the client CA.
// There, a connection whose certificate does not chain to that CA fails its
// handshake, before any request is read. With no front, any client completes
// the handshake and may speak HTTP/2.
func (c edgeTLSConfig) serverConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: edge TLS certificate: %w", err)
	}
	if c.direct() {
		return &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
		}, nil
	}
	caPEM, err := os.ReadFile(c.clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: edge TLS client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("gatewayd-public: edge TLS client CA %s holds no PEM certificate", c.clientCAFile)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		// Cloudflare speaks HTTP/1.1 to an origin unless the zone turns on
		// HTTP/2 to origin; this listener offers HTTP/1.1 only.
		NextProtos: []string{"http/1.1"},
	}, nil
}

// openEdgeTLSListener claims the listener's TCP socket, which a hand-off
// passes on like the others, and serves TLS on it.
func openEdgeTLSListener(c edgeTLSConfig) (net.Listener, error) {
	cfg, err := c.serverConfig()
	if err != nil {
		return nil, err
	}
	ln, err := wire.ListenInherited("tcp", c.addr)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: listen edge TLS %s: %w", c.addr, err)
	}
	return tls.NewListener(ln, cfg), nil
}

// attach makes srv trust the TLS listener's connections and count its failed
// handshakes, and with no front lets it serve HTTP/2 over TLS. srv keeps its
// defaults while the listener is off.
func (c edgeTLSConfig) attach(srv *http.Server, log *slog.Logger, failures prometheus.Counter) {
	if !c.enabled() {
		return
	}
	srv.ConnContext = markVerifiedIngress
	srv.ErrorLog = stdlog.New(newEdgeServerErrors(log, failures), "", 0)
	if c.direct() && srv.Protocols != nil {
		srv.Protocols.SetHTTP2(true)
	}
}

// markVerifiedIngress is the public server's ConnContext. Only the TLS
// listener yields *tls.Conn. Behind Cloudflare it verifies the client
// certificate before a request is read; the gateway checks that certificate
// again per request, so a direct client's connection, which has none, is
// marked but trusts nothing.
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
	e.failures.Inc()
	now := e.now().UnixNano()
	last := e.lastLog.Load()
	if now-last >= int64(time.Minute) && e.lastLog.CompareAndSwap(last, now) {
		e.log.Warn("gatewayd-public: edge TLS handshake failed", "err", msg)
	}
	return len(p), nil
}
