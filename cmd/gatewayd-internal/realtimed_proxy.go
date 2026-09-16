package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// newRealtimedProxy builds an HTTP/1.1 reverse proxy over the local Unix
// socket. httputil.ReverseProxy preserves Upgrade requests, so the same hop
// carries the WebSocket handshake and the hijacked connection after the
// handshake. An empty socket disables managed realtime integration.
func newRealtimedProxy(socket string, log *slog.Logger) http.Handler {
	if socket == "" {
		return nil
	}
	target, err := url.Parse("http://realtimed")
	if err != nil {
		if log != nil {
			log.Warn("managed realtime proxy disabled: invalid target", "err", err)
		}
		return nil
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 0,
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, proxyErr error) {
		if log != nil {
			log.Warn("managed realtime proxy failed", "path", r.URL.Path, "err", proxyErr)
		}
		http.Error(w, fmt.Sprintf("managed realtime unavailable: %v", proxyErr), http.StatusServiceUnavailable)
	}
	return proxy
}

// newRealtimedControlProxy exposes only the private realtimed management
// routes through gatewayd-internal's node listener. The public API uses this
// hop when a connection lives on another compute node; the listener itself is
// restricted to control-plane/mesh addresses by the deployment firewall.
//
// Incoming paths are rooted at /v1/internal/realtime and are rewritten to
// realtimed's /internal namespace. Keeping the rewrite here means the
// realtime client can use the same path contract over Unix and TCP transports.
func newRealtimedControlProxy(socket string, log *slog.Logger) http.Handler {
	if socket == "" {
		return nil
	}
	target, err := url.Parse("http://realtimed")
	if err != nil {
		if log != nil {
			log.Warn("managed realtime control proxy disabled: invalid target", "err", err)
		}
		return nil
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
		ForceAttemptHTTP2:   false,
		DisableCompression:  true,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
	}
	prefix := "/v1/internal/realtime"
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.FlushInterval = -1
	proxy.Director = func(req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, prefix)
		if path == "" {
			path = "/"
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		req.URL.Path = path
		req.URL.RawPath = ""
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, proxyErr error) {
		if log != nil {
			log.Warn("managed realtime control proxy failed", "path", r.URL.Path, "err", proxyErr)
		}
		http.Error(w, fmt.Sprintf("managed realtime unavailable: %v", proxyErr), http.StatusServiceUnavailable)
	}
	return proxy
}
