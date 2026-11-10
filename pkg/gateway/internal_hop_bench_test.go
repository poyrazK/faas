package gateway

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkInternalHop measures the edge → gatewayd-internal hop alone: a GET
// answered 404 with a small problem body, carrying the headers the compute
// edge adds. It compares calling the handler in the same process with the
// transports the edge can use over a unix socket: HTTP/1.1 (today), h2c, and a
// minimal length-prefixed framing that skips HTTP text parsing (a prototype,
// not a protocol: one request per connection at a time, no bodies, no
// upgrades). Both ends run in the benchmark, so with -cpu 1 ns/op is the hop's
// whole CPU cost per request (ADR-20261110081523).
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

	b.Run("unix_framed", func(b *testing.B) {
		path, ln := listenHopSocket(b)
		go serveFramedHop(ln, handler)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			conn, err := net.Dial("unix", path)
			if err != nil {
				b.Error(err)
				return
			}
			defer conn.Close()
			c := &framedHopClient{conn: conn, br: bufio.NewReader(conn)}
			for pb.Next() {
				status, _, err := c.roundTrip(http.MethodGet, host, "/", edgeHeaders)
				if err != nil {
					b.Error(err)
					return
				}
				if status != http.StatusNotFound {
					b.Errorf("status %d, want 404", status)
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

// The framed prototype: every frame is a 4-byte big-endian length, then
// uint16-length-prefixed strings. A request is method, host, path, a header
// count and the header pairs; a response is the status, a header count, the
// header pairs and the body.

var errShortFrame = errors.New("short hop frame")

func appendHopString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}

func appendHopHeaders(b []byte, h http.Header) []byte {
	n := 0
	for _, vs := range h {
		n += len(vs)
	}
	b = binary.BigEndian.AppendUint16(b, uint16(n))
	for k, vs := range h {
		for _, v := range vs {
			b = appendHopString(b, k)
			b = appendHopString(b, v)
		}
	}
	return b
}

type hopFrameReader struct{ b []byte }

func (r *hopFrameReader) u16() (int, error) {
	if len(r.b) < 2 {
		return 0, errShortFrame
	}
	v := int(binary.BigEndian.Uint16(r.b))
	r.b = r.b[2:]
	return v, nil
}

func (r *hopFrameReader) str() (string, error) {
	n, err := r.u16()
	if err != nil || len(r.b) < n {
		return "", errShortFrame
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s, nil
}

func (r *hopFrameReader) headers() (http.Header, error) {
	n, err := r.u16()
	if err != nil {
		return nil, err
	}
	h := make(http.Header, n)
	for range n {
		k, err := r.str()
		if err != nil {
			return nil, err
		}
		v, err := r.str()
		if err != nil {
			return nil, err
		}
		h[k] = append(h[k], v)
	}
	return h, nil
}

func readHopFrame(br *bufio.Reader, buf []byte) ([]byte, error) {
	var lenb [4]byte
	if _, err := io.ReadFull(br, lenb[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint32(lenb[:]))
	if cap(buf) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	_, err := io.ReadFull(br, buf)
	return buf, err
}

func finishHopFrame(b []byte) []byte {
	binary.BigEndian.PutUint32(b[:4], uint32(len(b)-4))
	return b
}

func serveFramedHop(ln net.Listener, h http.Handler) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go serveFramedHopConn(conn, h)
	}
}

func serveFramedHopConn(conn net.Conn, h http.Handler) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	var in, out []byte
	w := &framedHopResponseWriter{header: http.Header{}}
	for {
		var err error
		if in, err = readHopFrame(br, in); err != nil {
			return
		}
		req, err := decodeFramedHopRequest(in)
		if err != nil {
			return
		}
		clear(w.header)
		w.status, w.body = 0, w.body[:0]
		h.ServeHTTP(w, req)
		if w.status == 0 {
			w.status = http.StatusOK
		}
		out = binary.BigEndian.AppendUint16(append(out[:0], 0, 0, 0, 0), uint16(w.status))
		out = appendHopHeaders(out, w.header)
		out = finishHopFrame(append(out, w.body...))
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}

func decodeFramedHopRequest(frame []byte) (*http.Request, error) {
	r := &hopFrameReader{b: frame}
	method, err := r.str()
	if err != nil {
		return nil, err
	}
	host, err := r.str()
	if err != nil {
		return nil, err
	}
	path, err := r.str()
	if err != nil {
		return nil, err
	}
	u, err := url.ParseRequestURI(path)
	if err != nil {
		return nil, err
	}
	header, err := r.headers()
	if err != nil {
		return nil, err
	}
	return &http.Request{
		Method: method, Host: host, URL: u, Header: header,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Body: http.NoBody,
	}, nil
}

type framedHopResponseWriter struct {
	header http.Header
	status int
	body   []byte
}

func (w *framedHopResponseWriter) Header() http.Header { return w.header }
func (w *framedHopResponseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *framedHopResponseWriter) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	w.body = append(w.body, p...)
	return len(p), nil
}

type framedHopClient struct {
	conn    net.Conn
	br      *bufio.Reader
	out, in []byte
}

// roundTrip sends one request and parses the response's status and headers,
// as the edge must to copy them to its client; the body stays in the buffer.
func (c *framedHopClient) roundTrip(method, host, path string, h http.Header) (int, http.Header, error) {
	out := append(c.out[:0], 0, 0, 0, 0)
	out = appendHopString(out, method)
	out = appendHopString(out, host)
	out = appendHopString(out, path)
	c.out = finishHopFrame(appendHopHeaders(out, h))
	if _, err := c.conn.Write(c.out); err != nil {
		return 0, nil, err
	}
	var err error
	if c.in, err = readHopFrame(c.br, c.in); err != nil {
		return 0, nil, err
	}
	r := &hopFrameReader{b: c.in}
	status, err := r.u16()
	if err != nil {
		return 0, nil, err
	}
	header, err := r.headers()
	return status, header, err
}
