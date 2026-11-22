package binhop

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/binhttp"
)

// rawPeer answers one request on a pipe with whatever respond writes, so a
// test can send the client a response no well-behaved server would.
func rawPeer(t *testing.T, respond func(w *bufio.Writer)) func(context.Context) (net.Conn, error) {
	t.Helper()
	return func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			r := bufio.NewReader(server)
			head, err := binhttp.ReadRequestHead(r, 1<<16)
			if err != nil {
				return
			}
			if !head.NoContent {
				_, _ = io.Copy(io.Discard, binhttp.NewChunkReader(r))
				_, _ = binhttp.ReadTrailers(r, 1<<16)
			}
			w := bufio.NewWriter(server)
			respond(w)
			_ = w.Flush()
			_, _ = io.Copy(io.Discard, server)
		}()
		return client, nil
	}
}

func writeResponse(w *bufio.Writer, status int, header http.Header, body string) {
	_ = binhttp.WriteResponseStart(w)
	_ = binhttp.WriteResponseHead(w, status, header, nil)
	cw := binhttp.NewChunkWriter(w)
	if body != "" {
		_, _ = cw.Write([]byte(body))
	}
	_ = cw.Close()
	_ = binhttp.WriteTrailers(w, nil)
}

func getVia(t *testing.T, method string, dial func(context.Context) (net.Conn, error)) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, method, "http://peer/", http.NoBody)
	resp, err := NewTransport(time.Minute, 0, 0).RoundTrip(req, dial)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// A peer's response body must match its Content-Length, as net/http's client
// requires: a vmmd reading a bridge (or an edge reading gatewayd-internal)
// never passes on more or less than the head declared.
func TestTransportHoldsResponseToContentLength(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		header  http.Header
		body    string
		wantErr error
		want    string
	}{
		{name: "exact", method: http.MethodGet, header: http.Header{"Content-Length": {"3"}}, body: "abc", want: "abc"},
		{name: "longer", method: http.MethodGet, header: http.Header{"Content-Length": {"3"}}, body: "abcdef", wantErr: errBodyTooLong},
		{name: "shorter", method: http.MethodGet, header: http.Header{"Content-Length": {"10"}}, body: "abc", wantErr: io.ErrUnexpectedEOF},
		{name: "no length", method: http.MethodGet, header: http.Header{}, body: "abcdef", want: "abcdef"},
		{name: "head declares the GET body", method: http.MethodHead, header: http.Header{"Content-Length": {"10"}}, body: "", want: ""},
		{name: "repeated equal values", method: http.MethodGet, header: http.Header{"Content-Length": {"3", "3"}}, body: "abc", want: "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getVia(t, tt.method, rawPeer(t, func(w *bufio.Writer) { writeResponse(w, 200, tt.header, tt.body) }))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("body %q err %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestTransportRefusesConflictingContentLength(t *testing.T) {
	for _, values := range [][]string{{"3", "4"}, {"3, 4"}, {"-1"}, {"+3"}, {"3x"}, {""}} {
		_, err := getVia(t, http.MethodGet, rawPeer(t, func(w *bufio.Writer) {
			writeResponse(w, 200, http.Header{"Content-Length": values}, "abc")
		}))
		if err == nil || !strings.Contains(err.Error(), "Content-Length") {
			t.Fatalf("Content-Length %q: err %v, want a Content-Length error", values, err)
		}
	}
}

// A peer cannot keep the client reading informational heads forever.
func TestTransportBoundsInformationalResponses(t *testing.T) {
	respond := func(n int) func(w *bufio.Writer) {
		return func(w *bufio.Writer) {
			_ = binhttp.WriteResponseStart(w)
			for range n {
				_ = binhttp.WriteResponseHead(w, http.StatusEarlyHints, http.Header{"Link": {"</a>"}}, nil)
			}
			_ = binhttp.WriteResponseHead(w, 200, http.Header{}, nil)
			_ = binhttp.NewChunkWriter(w).Close()
			_ = binhttp.WriteTrailers(w, nil)
		}
	}
	if _, err := getVia(t, http.MethodGet, rawPeer(t, respond(max1xxResponses))); err != nil {
		t.Fatalf("%d informational responses: %v", max1xxResponses, err)
	}
	if _, err := getVia(t, http.MethodGet, rawPeer(t, respond(max1xxResponses+1))); err == nil || !strings.Contains(err.Error(), "1xx") {
		t.Fatalf("%d informational responses: err %v, want a 1xx error", max1xxResponses+1, err)
	}
}

// The server holds a request body to its Content-Length and refuses
// conflicting values, so a handler (the stream bridge, gatewayd-internal)
// never reads a body its peer framed differently.
func TestServerHoldsRequestToContentLength(t *testing.T) {
	var read string
	var readErr error
	srv := &Server{Handler: func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			read, readErr = string(b), err
			w.WriteHeader(http.StatusNoContent)
		})
	}, ReadHeaderTimeout: 5 * time.Second}
	dir, err := os.MkdirTemp("", "bh") // unix socket paths are short on macOS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	send := func(header http.Header, body string) int {
		client, err := net.Dial("unix", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		w := bufio.NewWriter(client)
		go func() {
			_ = binhttp.WriteRequestHead(w, binhttp.RequestHead{Method: "POST", Scheme: "http", Authority: "a", Path: "/", Header: header})
			cw := binhttp.NewChunkWriter(w)
			_, _ = cw.Write([]byte(body))
			_ = cw.Close()
			_ = binhttp.WriteTrailers(w, nil)
			_ = w.Flush()
		}()
		r := bufio.NewReader(client)
		if err := binhttp.ReadResponseStart(r); err != nil {
			return 0
		}
		status, _, err := binhttp.ReadResponseHead(r, 1<<16)
		if err != nil {
			return 0
		}
		return status
	}
	if st := send(http.Header{"Content-Length": {"3"}}, "abc"); st != http.StatusNoContent || read != "abc" || readErr != nil {
		t.Fatalf("exact: status %d read %q err %v", st, read, readErr)
	}
	read, readErr = "", nil
	_ = send(http.Header{"Content-Length": {"3"}}, "abcdef")
	if !errors.Is(readErr, errBodyTooLong) {
		t.Fatalf("longer body: handler read %q err %v, want errBodyTooLong", read, readErr)
	}
	if st := send(http.Header{"Content-Length": {"3", "4"}}, "abc"); st != http.StatusBadRequest {
		t.Fatalf("conflicting Content-Length: status %d, want 400", st)
	}
}
