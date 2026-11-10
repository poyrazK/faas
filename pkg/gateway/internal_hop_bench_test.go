package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkInternalHop measures the edge → gatewayd-internal hop alone: a GET
// answered 404 with a small problem body, carrying the headers the compute
// edge adds. It compares calling the handler in the same process with the
// transports the edge can use over a unix socket: HTTP/1.1, h2c and binary
// HTTP (ADR-20261110081523). Both ends run in the benchmark, so with -cpu 1
// ns/op is the hop's whole CPU cost per request.
func BenchmarkInternalHop(b *testing.B) {
	body := []byte(`{"type":"https://docs.gregale.dev/errors/app_not_found","title":"App not found","status":404,"code":"app_not_found"}`)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(body)
	})
	const host = "lt-edge-none.gregale.dev"
	edgeHeaders := http.Header{
		"User-Agent":            {"edgeload/1"},
		"Accept":                {"*/*"},
		"X-Forwarded-For":       {"203.0.113.7"},
		"X-Forwarded-Proto":     {"https"},
		"Traceparent":           {"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		"X-Faas-Edge-Placement": {"1"},
	}

	b.Run("in_process", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req, _ := http.NewRequest(http.MethodGet, "http://"+host+"/", nil)
				req.Header = edgeHeaders.Clone()
				handler.ServeHTTP(&discardResponseWriter{header: http.Header{}}, req)
			}
		})
	})

	for _, bc := range []struct {
		name string
		h2c  bool
	}{
		{"unix_http1", false},
		{"unix_h2c", true},
	} {
		b.Run(bc.name, func(b *testing.B) {
			path, ln := listenHopSocket(b)
			srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
			if bc.h2c {
				srv.Protocols = new(http.Protocols)
				srv.Protocols.SetHTTP1(true)
				srv.Protocols.SetUnencryptedHTTP2(true)
			}
			go func() { _ = srv.Serve(ln) }()
			defer srv.Close()
			var rt http.RoundTripper
			if bc.h2c {
				rt = newInternalProxyH2CTransport(NewUnixSocketDialer(path), time.Second)
			} else {
				rt = newInternalProxyTransport(NewUnixSocketDialer(path), time.Second)
			}
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					req, _ := http.NewRequest(http.MethodGet, "http://gatewayd-internal/", nil)
					req.Host = host
					req.Header = edgeHeaders.Clone()
					resp, err := rt.RoundTrip(req)
					if err != nil {
						b.Error(err)
						return
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusNotFound {
						b.Errorf("status %d, want 404", resp.StatusCode)
						return
					}
				}
			})
		})
	}

	b.Run("unix_binary", func(b *testing.B) {
		path, ln := listenHopSocket(b)
		srv := &BinaryHopServer{Handler: func() http.Handler { return handler }, ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = srv.Serve(ln) }()
		rt := newBinaryHopTransport(time.Second)
		defer rt.closeIdle()
		dial := func(ctx context.Context) (net.Conn, error) { return NewUnixSocketDialer(path).DialContext(ctx, "") }
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req, _ := http.NewRequest(http.MethodGet, "http://gatewayd-internal/", nil)
				req.Host = host
				req.Header = edgeHeaders.Clone()
				resp, err := rt.roundTrip(req, dial)
				if err != nil {
					b.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusNotFound {
					b.Errorf("status %d, want 404", resp.StatusCode)
					return
				}
			}
		})
	})
}

func listenHopSocket(b *testing.B) (string, net.Listener) {
	b.Helper()
	dir, err := os.MkdirTemp("", "hop")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = ln.Close() })
	return path, ln
}

type discardResponseWriter struct {
	header http.Header
}

func (w *discardResponseWriter) Header() http.Header         { return w.header }
func (w *discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *discardResponseWriter) WriteHeader(int)             {}
