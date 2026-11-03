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
// Off unless FAAS_EDGE_PUBLIC_TLS_ADDR is set.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
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

// edgeTLSConfigFromEnv reads the listener's settings. An address needs all
// three files, on either front, and files without an address are a mistake
// too: either way the edge would not serve what the operator meant. Only a
// compute edge takes them.
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
	var missing []string
	for _, setting := range []struct{ env, value string }{
		{edgeTLSAddrEnv, c.addr},
		{edgeTLSCertEnv, c.certFile},
		{edgeTLSKeyEnv, c.keyFile},
		{edgeTLSClientCAEnv, c.clientCAFile},
	} {
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

// errNotAnAppHost fails a handshake whose server name is missing or no app
// host's, before any HTTP work.
var errNotAnAppHost = errors.New("gatewayd-public: edge TLS server name is not an app host")

// serverConfig loads the certificate and the client CA. Behind Cloudflare, a
// connection whose certificate does not chain to that CA fails its handshake,
// before any request is read. With no front the certificate is verified only
// when given, the server name must be one appHost accepts, and the client may
// speak HTTP/2.
func (c edgeTLSConfig) serverConfig(appHost func(name string) bool) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: edge TLS certificate: %w", err)
	}
	caPEM, err := os.ReadFile(c.clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: edge TLS client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("gatewayd-public: edge TLS client CA %s holds no PEM certificate", c.clientCAFile)
	}
	if c.direct() {
		return &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
			ClientAuth:   tls.VerifyClientCertIfGiven,
			ClientCAs:    clientCAs,
			NextProtos:   []string{"h2", "http/1.1"},
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
// passes on like the others, and serves TLS on it, within the per-address
// limits when they apply. appHost names the server names a listener with no
// front completes a handshake for; failed counts a handshake the limits' gate
// saw fail. Handshakes in the gate end with ctx.
func openEdgeTLSListener(ctx context.Context, c edgeTLSConfig, appHost func(name string) bool, limits edgeClientLimits, failed func(net.Addr, error)) (net.Listener, error) {
	cfg, err := c.serverConfig(appHost)
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
