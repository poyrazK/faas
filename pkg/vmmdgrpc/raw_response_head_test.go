package vmmdgrpc

import (
	"bufio"
	"bytes"
	"io"
	"testing"
)

// Exercise the composed reader/parser boundary with the LF framing emitted
// by vmmd-raw-bridge, including WebSocket bytes read ahead with the head.
// adr: 080 — keep the raw Upgrade head separate from duplex frame bytes.
func TestRawBridgeReadHeadPreservesBridgeFraming(t *testing.T) {
	for _, tc := range []struct {
		name   string
		head   string
		body   []byte
		status int32
	}{
		{"upgrade", "HTTP/1.1 101 Switching Protocols\nUpgrade: websocket\nConnection: Upgrade\nSec-WebSocket-Accept: audit-accept\n\n", []byte{0x81, 4, 'p', 'i', 'n', 'g'}, 101},
		{"rejection", "HTTP/1.1 403 Forbidden\nContent-Length: 6\n\n", []byte("denied"), 403},
		{"ordinary", "HTTP/1.1 200 OK\nContent-Length: 4\n\n", []byte{'a', '\r', '\n', 0xff}, 200},
		{"no_headers", "HTTP/1.1 204 No Content\n\n", nil, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := append([]byte(tc.head), tc.body...)
			reader := bufio.NewReader(bytes.NewReader(input))
			response, err := rawBridgeReadHead(reader, "")
			if err != nil {
				t.Fatalf("parse actual bridge framing: %v", err)
			}
			if response.Status != tc.status {
				t.Fatalf("status = %d, want %d", response.Status, tc.status)
			}
			if len(response.Body) != 0 {
				t.Fatalf("head parser consumed response body: %x", response.Body)
			}
			rest, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(rest, tc.body) {
				t.Fatalf("response bytes = %x, err = %v; want %x", rest, err, tc.body)
			}
			if tc.status == 101 {
				values := make(map[string]string)
				for _, header := range response.Headers {
					values[header.Name] = header.Value
				}
				if values["Upgrade"] != "websocket" || values["Connection"] != "Upgrade" || values["Sec-WebSocket-Accept"] != "audit-accept" {
					t.Fatalf("upgrade headers = %v", values)
				}
			}
		})
	}
}

func TestRawBridgeReadHeadRejectsMalformedFraming(t *testing.T) {
	for _, input := range []string{
		"HTTP/1.1 101 Switching Protocols\nUpgrade: websocket\n",
		"HTTP/1.1 broken\n\n",
		"HTTP/1.1 700 Invalid\n\n",
		"HTTP/1.1 101 Switching Protocols\nX-Fill: " + string(bytes.Repeat([]byte{'x'}, 65*1024)) + "\n\n",
	} {
		if response, err := rawBridgeReadHead(bufio.NewReader(bytes.NewBufferString(input)), ""); err == nil || response != nil {
			t.Fatalf("malformed response accepted: %v", response)
		}
	}
}
