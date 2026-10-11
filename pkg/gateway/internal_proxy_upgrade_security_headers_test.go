package gateway

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/httpsec"
)

// ADR-830 on the 101 path: httputil appends the upstream headers after
// ModifyResponse, so the app's value must replace the default exactly once.
func TestInternalReverseProxy_UpgradeKeepsCustomerSecurityHeaders(t *testing.T) {
	httpsec.SetHSTSEnabled(true)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("backend hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nX-Frame-Options: SAMEORIGIN\r\n\r\n")
		_ = rw.Flush()
		time.Sleep(100 * time.Millisecond)
	}))
	defer backend.Close()
	proxy := NewInternalReverseProxy(&stubDialer{server: backend}, &url.URL{Scheme: "http", Host: "gatewayd-internal"}, slog.Default(), true)
	public := httptest.NewServer(httpsec.ForSurface(func(r *http.Request) httpsec.Surface {
		return httpsec.ClassifyHost(r.Host, "gregale.dev")
	}, true, proxy))
	defer public.Close()

	conn, err := net.DialTimeout("tcp", public.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	if got := resp.Header.Values("X-Frame-Options"); len(got) != 1 || got[0] != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want the app's single value", got)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got != httpsec.ValueHSTSCustomDomain {
		t.Errorf("HSTS = %q, want the custom-domain default", got)
	}
}
