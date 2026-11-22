package binhop

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/binhttp"
)

// FuzzTransportResponse feeds the client whatever bytes a peer process
// might send (vmmd's view of a compromised stream bridge): it must not
// panic or hang, and a response it accepts never yields more content than
// its Content-Length allows.
func FuzzTransportResponse(f *testing.F) {
	var ok bytes.Buffer
	w := bufio.NewWriter(&ok)
	writeResponse(w, 200, http.Header{"Content-Length": {"3"}}, "abc")
	_ = w.Flush()
	f.Add(ok.Bytes())
	f.Add([]byte{3, 0x40, 0xc8, 0, 5, 'h', 'e', 'l', 'l', 'o', 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		dial := func(context.Context) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				// Read the request, answer with b, then hang up, so an
				// incomplete answer ends the read instead of waiting.
				r := bufio.NewReader(server)
				if _, err := binhttp.ReadRequestHead(r, 1<<16); err == nil {
					_, _ = server.Write(b)
				}
				_ = server.Close()
			}()
			return client, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://peer/", http.NoBody)
		resp, err := NewTransport(time.Minute, 4096, 0).RoundTrip(req, dial)
		if err != nil {
			return
		}
		defer func() { _ = resp.Body.Close() }()
		n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		if declared := resp.Header.Get("Content-Length"); err == nil && declared != "" {
			if want, perr := strconv.ParseInt(declared, 10, 64); perr == nil && n != want {
				t.Fatalf("read %d bytes of a %d-byte body without an error", n, want)
			}
		}
	})
}
