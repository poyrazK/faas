package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
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
