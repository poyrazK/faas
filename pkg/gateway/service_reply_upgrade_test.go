package gateway

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// Billing review R8-2: on an upgraded service call the raw bridge records the
// bytes the callee streams back as service reply bytes as well as response
// bytes; a public upgrade records response bytes only.
func TestRawUpgradeRecordsServiceReplyBytesOnlyForServiceCalls(t *testing.T) {
	for _, service := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "service call"}[service], func(t *testing.T) {
			sink := egresssink.NewEgressSink()
			target := Target{Port: 3000, InstanceID: "callee-1"}
			finished := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				ctx, cancelBudget, _ := reqbudget.WithRemaining(ctx, 100*time.Millisecond, 100*time.Millisecond, "upgrade", "GET:/ws")
				defer cancelBudget()
				if service {
					ctx = withServiceReplyAccounting(ctx)
				}
				recorder := &statusRecorder{ResponseWriter: w, status: 200}
				rawStreamOnceWithEvents(recorder, r.WithContext(ctx), &upgradeWireClient{}, slog.Default(), target, nil, sink)
			}))
			defer server.Close()
			conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte{0x82, 0x7e, 0x0d, 0x0a}, 512)
			head := []byte("GET /ws HTTP/1.1\r\nHost: wire.example\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
			if _, err := conn.Write(append(head, payload...)); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
			if err != nil {
				t.Fatalf("upgrade response: %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusSwitchingProtocols {
				t.Fatalf("upgrade status = %d, want 101", response.StatusCode)
			}
			if _, err := io.ReadFull(reader, make([]byte, len(payload))); err != nil {
				t.Fatalf("upgraded bytes did not round trip: %v", err)
			}
			_ = conn.Close()
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("handler did not finish after the client closed")
			}
			var responseBytes, replyBytes uint64
			for _, record := range sink.Snapshot() {
				responseBytes += record.Bytes
				replyBytes += record.ServiceReplyBytes
			}
			wantReply := uint64(0)
			if service {
				wantReply = uint64(len(payload))
			}
			if responseBytes != uint64(len(payload)) || replyBytes != wantReply {
				t.Fatalf("recorded response %d, service reply %d; want %d and %d", responseBytes, replyBytes, len(payload), wantReply)
			}
		})
	}
}
