package main

// adr: 20261122140000 — vmmd sends an HTTP/1.1 app's requests to its stream
// bridge in binary HTTP (pkg/binhop) instead of h2c, on the same socket.

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/binhop"
	"github.com/onebox-faas/faas/pkg/binhttp"
)

// bridgeBinaryHTTPEnv is set to 1 by vmmd when it will send this bridge's
// requests in binary HTTP (FAAS_VMMD_BRIDGE_BINARY_HTTP). The bridge then
// serves binary HTTP beside h2c; without it, it serves h2c alone as before.
const bridgeBinaryHTTPEnv = "FAAS_BRIDGE_BINARY_HTTP"

// binaryPeekTimeout bounds how long a new connection may take to send its
// first byte, as the session split's hello timeout does.
const binaryPeekTimeout = 10 * time.Second

func binaryHTTPEnabled() bool { return os.Getenv(bridgeBinaryHTTPEnv) == "1" }

// newBinaryHopServer serves handler, the same one h2c serves, with the h2c
// server's limits: header size (MaxHeaderBytes, 1 MiB), the header read
// timeout as the slowloris defence, the idle timeout, and no read or write
// timeout, so uploads and streamed responses run as long as on h2c.
func newBinaryHopServer(handler http.Handler) *binhop.Server {
	return &binhop.Server{
		Handler:           func() http.Handler { return handler },
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       api.StreamBridgeIdleTimeout,
		MaxHeaderBytes:    int(api.DefaultMaxHeaderBytes),
	}
}

// splitBinaryHTTP hands each connection of ln that starts a binary HTTP
// request to binary and every other one (h2c) to the returned listener,
// with the peeked byte replayed.
func splitBinaryHTTP(ln net.Listener) (h2 net.Listener, binary net.Listener) {
	s := &binarySplit{src: ln, done: make(chan struct{})}
	s.h2 = &splitSide{s: s, conns: make(chan net.Conn)}
	s.bin = &splitSide{s: s, conns: make(chan net.Conn)}
	go s.accept()
	return s.h2, s.bin
}

type binarySplit struct {
	src     net.Listener
	h2, bin *splitSide
	done    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	err     error
}

func (s *binarySplit) accept() {
	for {
		conn, err := s.src.Accept()
		if err != nil {
			s.stop(err)
			return
		}
		go s.route(conn)
	}
}

func (s *binarySplit) route(conn net.Conn) {
	r := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(binaryPeekTimeout))
	first, err := r.Peek(1)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return
	}
	side := s.h2
	if binhttp.IsRequestStart(first[0]) {
		side = s.bin
	}
	select {
	case side.conns <- &replayedConn{Conn: conn, r: r}:
	case <-s.done:
		_ = conn.Close()
	}
}

func (s *binarySplit) stop(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
	})
}

// splitSide is one of the two listeners a binarySplit feeds.
type splitSide struct {
	s     *binarySplit
	conns chan net.Conn
}

func (l *splitSide) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.s.done:
		l.s.mu.Lock()
		defer l.s.mu.Unlock()
		if l.s.err == nil {
			return nil, net.ErrClosed
		}
		return nil, l.s.err
	}
}

// Close closes the shared listener: both sides stop.
func (l *splitSide) Close() error {
	err := l.s.src.Close()
	l.s.stop(net.ErrClosed)
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (l *splitSide) Addr() net.Addr { return l.s.src.Addr() }

// replayedConn reads the peeked byte before the connection.
type replayedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *replayedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// CloseWrite keeps half-closes working through the wrapper.
func (c *replayedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// serveBinaryHTTP serves handler in binary HTTP on its share of ln. It
// returns the listener h2c serves and the binary HTTP server, which the
// caller shuts down beside the h2c one.
func serveBinaryHTTP(ln net.Listener, handler http.Handler) (net.Listener, *binhop.Server) {
	h2, bin := splitBinaryHTTP(ln)
	srv := newBinaryHopServer(handler)
	go func() { _ = srv.Serve(bin) }()
	return h2, srv
}
