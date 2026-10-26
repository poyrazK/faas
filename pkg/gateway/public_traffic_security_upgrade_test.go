// adr: 570
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
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

// A real WebSocket upgrade from an N-1 compute gateway carries no traffic
// security snapshot. While the edge's peer gate says a gateway it may dial
// predates the snapshot, the 101 becomes a live two-way tunnel that outlives
// the request budget; once every gateway stamps it, the same unstamped 101 is
// refused before the edge hijacks the client connection.
func TestPublicTrafficSecurityRealUpgradeFollowsItsPeers(t *testing.T) {
	for _, required := range []bool{false, true} {
		t.Run("required="+strconv.FormatBool(required), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("backend hijack: %v", err)
					return
				}
				defer conn.Close()
				_, _ = fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
				if err := rw.Flush(); err != nil {
					return
				}
				var request [4]byte
				if _, err := io.ReadFull(rw, request[:]); err != nil {
					return
				}
				_, _ = rw.WriteString("pong")
				_ = rw.Flush()
			}))
			defer backend.Close()
			registry := trafficrevocation.New(&gatewaySecurityStore{})
			defer registry.Close()
			peers := &fakeTrafficSecurityPeers{required: required}
			proxy := NewInternalReverseProxy(&stubDialer{server: backend}, &url.URL{Scheme: "http", Host: "gatewayd-internal"}, slog.Default(), true).
				WithTrafficRevocations(registry).WithTrafficSecurityPeers(peers)
			budget := reqbudget.MiddlewareConfig{Default: 200 * time.Millisecond, Max: time.Second, Route: "forward", Endpoint: "test"}
			public := httptest.NewServer(httpsec.Static(budget.Middleware(proxy)))
			defer public.Close()

			conn, err := net.DialTimeout("tcp", public.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			_, _ = fmt.Fprint(conn, "GET /socket HTTP/1.1\r\nHost: app.gregale.dev\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
			reader := bufio.NewReader(conn)
			resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.Header.Get(trafficSecurityHeader) != "" {
				t.Fatalf("the snapshot reached the client: %v", resp.Header)
			}
			if required {
				if resp.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, want 503 for an unstamped upgrade once every gateway stamps", resp.StatusCode)
				}
				return
			}
			if resp.StatusCode != http.StatusSwitchingProtocols {
				t.Fatalf("status = %d, want 101 from an N-1 gateway", resp.StatusCode)
			}
			// The 200 ms request budget must not cut the tunnel.
			time.Sleep(300 * time.Millisecond)
			_, _ = conn.Write([]byte("ping"))
			var reply [4]byte
			if _, err := io.ReadFull(reader, reply[:]); err != nil || string(reply[:]) != "pong" {
				t.Fatalf("tunnel reply = %q, %v", reply, err)
			}
			if peers.unverified.Load() != 1 {
				t.Fatalf("unverified answers = %d, want 1", peers.unverified.Load())
			}
			if n, scopes := registry.Tracked(); n != 0 || scopes != 0 {
				t.Fatalf("an unverified tunnel registered %d/%d", n, scopes)
			}
		})
	}
}
