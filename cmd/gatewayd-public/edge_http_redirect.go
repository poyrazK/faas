package main

// adr: 20261127170000 — plain HTTP on a compute edge's public entry
// addresses answers with a 308 to HTTPS.

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/apid"
	"github.com/onebox-faas/faas/pkg/wire"
)

// edgeHTTPRedirectAddrEnv is the plain-HTTP redirect listener's address, e.g.
// ":80". Before it, *.gregale.app resolved straight to the entry addresses,
// whose firewall dropped port 80, so `http://` hung until the client gave up
// (prod hunt r74).
const edgeHTTPRedirectAddrEnv = "FAAS_EDGE_PUBLIC_HTTP_REDIRECT_ADDR"

// The redirect listener reads one small request head and writes one small
// answer; these bound a client that does neither.
const (
	edgeRedirectReadHeaderTimeout = 5 * time.Second
	edgeRedirectIdleTimeout       = 30 * time.Second
	edgeRedirectMaxHeaderBytes    = 16 << 10
)

// edgeHTTPRedirectAddr reads the redirect listener's address. It needs the
// direct TLS listener: behind Cloudflare, Cloudflare answers port 80 itself.
func edgeHTTPRedirectAddr(tls edgeTLSConfig, getenv func(string) string) (string, error) {
	addr := strings.TrimSpace(getenv(edgeHTTPRedirectAddrEnv))
	if addr == "" {
		return "", nil
	}
	if !tls.enabled() || !tls.direct() {
		return "", fmt.Errorf("gatewayd-public: %s needs the direct edge TLS listener (%s and %s=%s)", edgeHTTPRedirectAddrEnv, edgeTLSAddrEnv, edgeTLSFrontEnv, edgeFrontNone)
	}
	return addr, nil
}

// httpsRedirect answers every request with a 308 to the same host and path
// over HTTPS. 308 keeps the method and body, so a POST is not turned into a
// GET. A missing host, an IP literal or a platform host gets a 400 or 404:
// those are served through the control plane's edge, not here.
func httpsRedirect(platformDomain string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		host = strings.TrimSuffix(host, ".")
		w.Header().Set("Connection", "close")
		switch {
		case host == "" || strings.ContainsAny(host, "/@\\ ") || r.URL.Host != "":
			http.Error(w, "this address serves HTTPS app hosts; send a Host header", http.StatusBadRequest)
			return
		case net.ParseIP(host) != nil || apid.IsPlatformHost(host, platformDomain):
			http.Error(w, "no app is served here over plain HTTP", http.StatusNotFound)
			return
		}
		w.Header().Set("Location", "https://"+host+r.URL.RequestURI())
		w.WriteHeader(http.StatusPermanentRedirect)
	})
}

// startEdgeHTTPRedirect opens and serves the redirect listener until ctx
// ends. The socket is shared with a hand-off successor like the others, so
// both generations may answer redirects while they overlap. It returns a
// stop func that closes the server; nil addr starts nothing.
func startEdgeHTTPRedirect(ctx context.Context, addr, platformDomain string, log *slog.Logger) (func(), error) {
	if addr == "" {
		return func() {}, nil
	}
	ln, err := wire.ListenInherited("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: listen http redirect %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           httpsRedirect(platformDomain),
		ReadHeaderTimeout: edgeRedirectReadHeaderTimeout,
		ReadTimeout:       edgeRedirectReadHeaderTimeout,
		WriteTimeout:      edgeRedirectReadHeaderTimeout,
		IdleTimeout:       edgeRedirectIdleTimeout,
		MaxHeaderBytes:    edgeRedirectMaxHeaderBytes,
		ErrorLog:          stdlog.New(io.Discard, "", 0),
	}
	go func() {
		log.Info("gatewayd-public: http redirect listening", "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("gatewayd-public: http redirect stopped", "err", err)
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = srv.Close() })
	return func() {
		stop()
		_ = srv.Close()
	}, nil
}
