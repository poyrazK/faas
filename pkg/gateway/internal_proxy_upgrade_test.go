package gateway

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// This is the actual public-to-internal composition, not a mocked RoundTrip:
// an HTTP/1.1 client enters through the public listener while the proxy's
// ordinary transport is configured for H2C. A 101 must still use HTTP/1.1
// to the backend and become a two-way byte tunnel.
func TestInternalReverseProxy_UpgradeTunnelThroughPublicStack(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Proto != "HTTP/1.1" || r.Host != "app.gregale.dev" {
			t.Errorf("backend protocol/host = %q/%q", r.Proto, r.Host)
		}
		if got := r.Header.Get("X-Forwarded-For"); got != "127.0.0.1" {
			t.Errorf("forwarded client IP = %q, want untrusted peer not spoofed value", got)
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("backend hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nX-Frame-Options: unsafe\r\n\r\n")
		if err := rw.Flush(); err != nil {
			t.Errorf("backend flush: %v", err)
			return
		}
		var request [4]byte
		if _, err := io.ReadFull(rw, request[:]); err != nil {
			t.Errorf("backend read: %v", err)
			return
		}
		if string(request[:]) != "ping" {
			t.Errorf("backend payload = %q", request)
		}
		_, _ = rw.WriteString("pong")
		_ = rw.Flush()
	}))
	defer backend.Close()

	proxy := NewInternalReverseProxy(&stubDialer{server: backend},
		&url.URL{Scheme: "http", Host: "gatewayd-internal"}, slog.Default(), true)
	budget := reqbudget.MiddlewareConfig{Default: 200 * time.Millisecond, Max: time.Second, Route: "forward", Endpoint: "test"}
	public := httptest.NewServer(httpsec.Static(budget.Middleware(otelhttp.NewHandler(proxy, "public"))))
	defer public.Close()

	conn, err := net.DialTimeout("tcp", public.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprint(conn, "GET /__gregale/realtime/endpoint HTTP/1.1\r\nHost: app.gregale.dev\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nX-Forwarded-For: 203.0.113.7\r\n\r\n")
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	if got := resp.Header.Get("Connection"); !strings.EqualFold(got, "upgrade") {
		t.Errorf("Connection = %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got == "unsafe" || got == "" {
		t.Errorf("public policy header = %q", got)
	}
	// The ordinary 200 ms request budget must not kill a live tunnel.
	time.Sleep(300 * time.Millisecond)
	_, _ = conn.Write([]byte("ping"))
	var reply [4]byte
	if _, err := io.ReadFull(reader, reply[:]); err != nil {
		t.Fatal(err)
	}
	if string(reply[:]) != "pong" {
		t.Fatalf("tunnel reply = %q", reply)
	}
}

func TestInternalReverseProxy_UpgradeRefusalsReturnImmediately(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusUpgradeRequired} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Refusal", "from-app")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "not upgrading")
			}))
			defer backend.Close()
			proxy := NewInternalReverseProxy(&stubDialer{server: backend},
				&url.URL{Scheme: "http", Host: "gatewayd-internal"}, slog.Default(), true)
			public := httptest.NewServer(proxy)
			defer public.Close()
			req, err := http.NewRequest(http.MethodGet, public.URL+"/socket", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != status || resp.Header.Get("X-Refusal") != "from-app" || string(body) != "not upgrading" {
				t.Fatalf("response = %d %q %q", resp.StatusCode, resp.Header.Get("X-Refusal"), body)
			}
		})
	}
}
