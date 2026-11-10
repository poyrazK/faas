package gateway

// adr: 20261110081523 — gatewayd-internal serves its own node's compute edge
// over binary HTTP on a second unix socket.
//
// BinaryHopServer reads one binary HTTP request (pkg/binhttp) at a time from
// each connection, turns it into an *http.Request and serves it through the
// same handler the HTTP/1.1 listener runs, with the same limits: header
// size, read and write timeouts, idle timeout. What a handler sees matches
// net/http's server: the headers the edge sent, RemoteAddr, ContentLength 0
// and http.NoBody for a request with no content, trailers after the body's
// end, and a context cancelled when the edge closes the connection. The
// response side buffers the first 2 KB, sniffs Content-Type and sets
// Content-Length for a small response, as net/http does. Flush sends a
// chunk. Hijack is not supported: upgrades stay on HTTP/1.1.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/binhttp"
)

// Sizes net/http uses on its server side.
const (
	// hopBufferBeforeChunking is how much of a response the server holds
	// before sending its head, so a small one gets a Content-Length.
	hopBufferBeforeChunking = 2048
	// hopMaxPostHandlerReadBytes is how much unread request body the server
	// discards after a handler to reuse the connection.
	hopMaxPostHandlerReadBytes = 256 << 10
	hopBufferSize              = 32 << 10
	// hopBackgroundReadDelay is how long a handler runs before the server
	// starts watching its connection for the edge going away. A request
	// answered sooner never pays for the watch, and its cancellation could
	// not have saved much work.
	hopBackgroundReadDelay = 2 * time.Millisecond
	// sniffLen is how much of a body http.DetectContentType reads.
	sniffLen = 512
)

// aLongTimeAgo is a deadline in the past, which interrupts a blocked read.
var aLongTimeAgo = time.Unix(1, 0)

// BinaryHopServer serves Handler to binary HTTP connections. The zero
// timeouts mean none, as in http.Server.
type BinaryHopServer struct {
	// Handler returns the handler for each request, so a caller may set it
	// after the server is built.
	Handler           func() http.Handler
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	Log               *slog.Logger

	mu        sync.Mutex
	listeners map[net.Listener]struct{}
	conns     map[*hopServerConn]struct{}
	shutdown  atomic.Bool
	active    sync.WaitGroup
}

// Serve accepts connections on ln until it fails or the server shuts down.
func (s *BinaryHopServer) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.listeners == nil {
		s.listeners = map[net.Listener]struct{}{}
		s.conns = map[*hopServerConn]struct{}{}
	}
	s.listeners[ln] = struct{}{}
	s.mu.Unlock()
	var delay time.Duration
	for {
		c, err := ln.Accept()
		if err != nil {
			if s.shutdown.Load() {
				return http.ErrServerClosed
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				delay = min(max(2*delay, 5*time.Millisecond), time.Second)
				time.Sleep(delay)
				continue
			}
			return err
		}
		delay = 0
		hc := &hopServerConn{srv: s, conn: c, br: bufio.NewReaderSize(c, hopBufferSize), bw: bufio.NewWriterSize(c, hopBufferSize)}
		s.mu.Lock()
		if s.shutdown.Load() {
			s.mu.Unlock()
			_ = c.Close()
			return http.ErrServerClosed
		}
		s.conns[hc] = struct{}{}
		s.active.Add(1)
		s.mu.Unlock()
		go hc.serve()
	}
}

// Shutdown stops accepting connections, closes idle ones as they become
// idle, and waits for the requests in progress until ctx ends, when it
// closes the rest.
func (s *BinaryHopServer) Shutdown(ctx context.Context) error {
	s.shutdown.Store(true)
	s.mu.Lock()
	for ln := range s.listeners {
		_ = ln.Close()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.active.Wait()
		close(done)
	}()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		s.closeIdle()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			s.mu.Lock()
			for c := range s.conns {
				_ = c.conn.Close()
			}
			s.mu.Unlock()
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (s *BinaryHopServer) closeIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		if c.idle.Load() {
			_ = c.conn.Close()
		}
	}
}

func (s *BinaryHopServer) forget(c *hopServerConn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	s.active.Done()
}

func (s *BinaryHopServer) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *BinaryHopServer) maxHeaderBytes() int {
	if s.MaxHeaderBytes > 0 {
		return s.MaxHeaderBytes
	}
	return http.DefaultMaxHeaderBytes
}

// hopServerConn is one connection from the edge.
type hopServerConn struct {
	srv  *BinaryHopServer
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
	idle atomic.Bool

	// The background read watches the connection while a handler runs,
	// once its request has been read in full, and cancels the request when
	// the edge closes the connection. The request's end may be read on
	// another goroutine (a handler's proxy copying the body), so bgMu guards
	// starting and stopping it.
	bgMu      sync.Mutex
	bgStopped bool
	bgTimer   *time.Timer
	bgDone    chan struct{}
	bgAborted atomic.Bool
	bgByte    byte
	bgHas     bool
	bgErr     error
}

func (c *hopServerConn) serve() {
	defer c.srv.forget(c)
	defer func() { _ = c.conn.Close() }()
	for {
		c.idle.Store(true)
		if c.srv.shutdown.Load() {
			return
		}
		c.setReadDeadline(c.srv.IdleTimeout)
		if _, err := c.br.Peek(1); err != nil {
			return
		}
		c.idle.Store(false)
		if !c.serveOne() {
			return
		}
	}
}

func (c *hopServerConn) setReadDeadline(d time.Duration) {
	if d > 0 {
		_ = c.conn.SetReadDeadline(time.Now().Add(d))
	} else {
		_ = c.conn.SetReadDeadline(time.Time{})
	}
}

// serveOne reads and serves one request and reports whether the connection
// may carry another.
func (c *hopServerConn) serveOne() bool {
	start := time.Now()
	headerTimeout := c.srv.ReadHeaderTimeout
	if headerTimeout <= 0 {
		headerTimeout = c.srv.ReadTimeout
	}
	c.setReadDeadline(headerTimeout)
	head, err := binhttp.ReadRequestHead(c.br, c.srv.maxHeaderBytes())
	if err != nil {
		if errors.Is(err, binhttp.ErrHeaderTooLarge) {
			c.writeError(http.StatusRequestHeaderFieldsTooLarge)
		} else if !errors.Is(err, io.EOF) && !isTimeout(err) {
			c.writeError(http.StatusBadRequest)
		}
		return false
	}
	if c.srv.ReadTimeout > 0 {
		_ = c.conn.SetReadDeadline(start.Add(c.srv.ReadTimeout))
	} else {
		_ = c.conn.SetReadDeadline(time.Time{})
	}
	if c.srv.WriteTimeout > 0 {
		_ = c.conn.SetWriteDeadline(time.Now().Add(c.srv.WriteTimeout))
	} else {
		_ = c.conn.SetWriteDeadline(time.Time{})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.bgMu.Lock()
	c.bgStopped = false
	c.bgMu.Unlock()
	req, err := c.newRequest(ctx, head)
	if err != nil {
		c.writeError(http.StatusBadRequest)
		return false
	}
	var body *hopRequestBody
	if head.NoContent {
		c.armBackgroundRead(cancel)
	} else {
		body = &hopRequestBody{c: c, req: req, onEnd: func() { c.armBackgroundRead(cancel) }}
		body.cr.Reset(c.br)
		req.Body = body
	}
	w := &hopResponseWriter{c: c, req: req, header: make(http.Header, 4)}
	if !c.runHandler(w, req) {
		c.stopBackgroundRead()
		return false
	}
	w.finish()
	reuse := w.err == nil && !w.closeAfter
	if body != nil && !body.drain() {
		reuse = false
	}
	c.stopBackgroundRead()
	if c.bgErr != nil {
		reuse = false
	}
	return reuse && !c.srv.shutdown.Load()
}

// runHandler serves req and reports false when the handler panicked: the
// connection then closes with the response unfinished, as net/http does.
func (c *hopServerConn) runHandler(w *hopResponseWriter, req *http.Request) (ok bool) {
	defer func() {
		if v := recover(); v != nil {
			if v != http.ErrAbortHandler {
				c.srv.log().Error("gateway binary hop: handler panic", "err", v, "stack", string(debug.Stack()))
			}
			ok = false
		}
	}()
	h := c.srv.Handler()
	if h == nil {
		h = http.NotFoundHandler()
	}
	h.ServeHTTP(w, req)
	return true
}

func (c *hopServerConn) newRequest(ctx context.Context, head binhttp.RequestHead) (*http.Request, error) {
	if head.Method == "" || head.Path == "" {
		return nil, errors.New("gateway binary hop: request without a method or path")
	}
	u, err := url.ParseRequestURI(head.Path)
	if err != nil {
		return nil, fmt.Errorf("gateway binary hop: path %q: %w", head.Path, err)
	}
	header := head.Header
	if header == nil {
		header = http.Header{}
	}
	req := (&http.Request{
		Method:     head.Method,
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     header,
		Host:       head.Authority,
		RequestURI: head.Path,
		RemoteAddr: c.conn.RemoteAddr().String(),
		Body:       http.NoBody,
	}).WithContext(ctx)
	if head.NoContent {
		return req, nil
	}
	req.ContentLength = -1
	if v := header.Get("Content-Length"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("gateway binary hop: Content-Length %q", v)
		}
		req.ContentLength = n
	}
	for _, v := range header.Values("Trailer") {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				if req.Trailer == nil {
					req.Trailer = http.Header{}
				}
				req.Trailer[http.CanonicalHeaderKey(name)] = nil
			}
		}
	}
	return req, nil
}

// writeError answers a request the server could not read and ends the
// connection.
func (c *hopServerConn) writeError(status int) {
	_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
	if binhttp.WriteResponseStart(c.bw) != nil ||
		binhttp.WriteResponseHead(c.bw, status, http.Header{"Content-Length": {"0"}, "Connection": {"close"}}, nil) != nil {
		return
	}
	_ = binhttp.NewChunkWriter(c.bw).Close()
	_ = binhttp.WriteTrailers(c.bw, nil)
	_ = c.bw.Flush()
}

// armBackgroundRead starts the background read once the request has run
// for hopBackgroundReadDelay.
func (c *hopServerConn) armBackgroundRead(cancel context.CancelFunc) {
	c.bgMu.Lock()
	defer c.bgMu.Unlock()
	if c.bgStopped || c.bgTimer != nil {
		return
	}
	c.bgTimer = time.AfterFunc(hopBackgroundReadDelay, func() { c.startBackgroundRead(cancel) })
}

// startBackgroundRead watches the connection while the handler runs. Bytes
// the reader already holds mean the edge is still there.
func (c *hopServerConn) startBackgroundRead(cancel context.CancelFunc) {
	c.bgMu.Lock()
	defer c.bgMu.Unlock()
	if c.bgStopped || c.bgDone != nil || c.br.Buffered() > 0 {
		return
	}
	c.bgAborted.Store(false)
	c.bgHas, c.bgErr = false, nil
	_ = c.conn.SetReadDeadline(time.Time{})
	done := make(chan struct{})
	c.bgDone = done
	go func() {
		defer close(done)
		var b [1]byte
		n, err := c.conn.Read(b[:])
		if n == 1 {
			c.bgByte, c.bgHas = b[0], true
		}
		// After stopBackgroundRead any error is its own deadline.
		if err != nil && !c.bgAborted.Load() {
			c.bgErr = err
			cancel()
		}
	}()
}

// stopBackgroundRead ends the background read and keeps a byte it read for
// the next request.
func (c *hopServerConn) stopBackgroundRead() {
	c.bgMu.Lock()
	defer c.bgMu.Unlock()
	c.bgStopped = true
	if c.bgTimer != nil {
		c.bgTimer.Stop()
		c.bgTimer = nil
	}
	if c.bgDone == nil {
		return
	}
	c.bgAborted.Store(true)
	_ = c.conn.SetReadDeadline(aLongTimeAgo)
	<-c.bgDone
	c.bgDone = nil
	_ = c.conn.SetReadDeadline(time.Time{})
	if c.bgHas {
		c.br = bufio.NewReaderSize(io.MultiReader(bytes.NewReader([]byte{c.bgByte}), c.conn), hopBufferSize)
		c.bgHas = false
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// hopRequestBody is a request's chunked content, then its trailers.
type hopRequestBody struct {
	mu    sync.Mutex
	c     *hopServerConn
	req   *http.Request
	cr    binhttp.ChunkReader
	onEnd func()
	ended bool
	err   error
}

func (b *hopRequestBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.read(p)
}

func (b *hopRequestBody) read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.ended {
		return 0, io.EOF
	}
	n, err := b.cr.Read(p)
	if errors.Is(err, io.EOF) {
		trailer, terr := binhttp.ReadTrailers(b.c.br, b.c.srv.maxHeaderBytes())
		if terr != nil {
			b.err = terr
			return n, terr
		}
		for k, v := range trailer {
			if b.req.Trailer == nil {
				b.req.Trailer = http.Header{}
			}
			b.req.Trailer[k] = v
		}
		b.ended = true
		b.onEnd()
		return n, io.EOF
	}
	if err != nil {
		b.err = err
	}
	return n, err
}

func (b *hopRequestBody) Close() error { return nil }

// drain discards what the handler left unread, up to a bound, and reports
// whether the request ended so the connection can carry another.
func (b *hopRequestBody) drain() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	buf := make([]byte, 4096)
	for left := hopMaxPostHandlerReadBytes; !b.ended && b.err == nil && left > 0; {
		n, _ := b.read(buf[:min(len(buf), left)])
		left -= n
	}
	return b.ended
}

// hopResponseWriter writes one binary HTTP response.
type hopResponseWriter struct {
	c      *hopServerConn
	req    *http.Request
	header http.Header

	status      int
	wroteHeader bool
	started     bool
	headSent    bool
	handlerDone bool
	buf         []byte
	headLen     int // bytes a handler wrote for a HEAD request
	cw          binhttp.ChunkWriter
	closeAfter  bool
	err         error
}

func (w *hopResponseWriter) Header() http.Header { return w.header }

func (w *hopResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", code))
	}
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		w.start()
		w.check(binhttp.WriteResponseHead(w.c.bw, code, w.header, omitResponseField))
		w.check(w.c.bw.Flush())
		return
	}
	w.wroteHeader = true
	w.status = code
}

func (w *hopResponseWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !bodyAllowedForStatus(w.status) {
		return 0, http.ErrBodyNotAllowed
	}
	if w.req.Method == http.MethodHead {
		// Nothing is sent, but the first bytes still type the response and
		// the count still sets its Content-Length, as net/http does.
		if !w.headSent && len(w.buf) < sniffLen {
			w.buf = append(w.buf, p[:min(len(p), sniffLen-len(w.buf))]...)
		}
		w.headLen += len(p)
		return len(p), nil
	}
	if !w.headSent {
		if len(w.buf)+len(p) <= hopBufferBeforeChunking {
			w.buf = append(w.buf, p...)
			return len(p), nil
		}
		w.sendHead(p)
	}
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.cw.Write(p)
	w.check(err)
	return n, err
}

// Flush sends what the response holds.
func (w *hopResponseWriter) Flush() { _ = w.FlushError() }

// FlushError is Flush reporting a write error (http.ResponseController).
func (w *hopResponseWriter) FlushError() error {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.headSent {
		w.sendHead(nil)
	}
	if w.err != nil {
		return w.err
	}
	w.check(w.c.bw.Flush())
	return w.err
}

// SetReadDeadline and SetWriteDeadline set the connection's deadlines
// (http.ResponseController).
func (w *hopResponseWriter) SetReadDeadline(t time.Time) error  { return w.c.conn.SetReadDeadline(t) }
func (w *hopResponseWriter) SetWriteDeadline(t time.Time) error { return w.c.conn.SetWriteDeadline(t) }

// EnableFullDuplex is a no-op: a handler may always read the request while
// it writes the response.
func (w *hopResponseWriter) EnableFullDuplex() error { return nil }

func (w *hopResponseWriter) check(err error) {
	if err != nil && w.err == nil {
		w.err = err
	}
}

func (w *hopResponseWriter) start() {
	if !w.started {
		w.started = true
		w.check(binhttp.WriteResponseStart(w.c.bw))
	}
}

// omitResponseField leaves out of a response head the fields that belong
// to one connection, and the trailers, which go after the content as
// net/http sends them: keys set with http.TrailerPrefix and the names the
// Trailer field declares.
func omitResponseField(h http.Header, name string) bool {
	switch name {
	case "Connection", "Keep-Alive", "Proxy-Connection", "Transfer-Encoding":
		return true
	}
	if strings.HasPrefix(name, http.TrailerPrefix) {
		return true
	}
	for _, v := range h["Trailer"] {
		for _, declared := range strings.Split(v, ",") {
			if http.CanonicalHeaderKey(strings.TrimSpace(declared)) == name {
				return true
			}
		}
	}
	return false
}

// sendHead sends the final head. more is the write that overflowed the
// buffer, nil when the handler flushed or finished. What net/http's server
// adds goes in as extra fields, leaving the handler's map untouched.
func (w *hopResponseWriter) sendHead(more []byte) {
	var extra [3]binhttp.Field
	n := 0
	if strings.EqualFold(strings.TrimSpace(w.header.Get("Connection")), "close") {
		w.closeAfter = true
	}
	if w.closeAfter {
		// Tell the edge not to reuse the connection.
		extra[n] = binhttp.Field{Name: "Connection", Value: "close"}
		n++
	}
	if _, typed := w.header["Content-Type"]; !typed && bodyAllowedForStatus(w.status) {
		if sniff := w.sniffData(more); len(sniff) > 0 {
			extra[n] = binhttp.Field{Name: "Content-Type", Value: http.DetectContentType(sniff)}
			n++
		}
	}
	length := len(w.buf)
	if w.req.Method == http.MethodHead {
		length = w.headLen
	}
	if w.handlerDone && more == nil && w.header.Get("Content-Length") == "" && w.header.Get("Trailer") == "" &&
		bodyAllowedForStatus(w.status) && (w.req.Method != http.MethodHead || length > 0) {
		extra[n] = binhttp.Field{Name: "Content-Length", Value: strconv.Itoa(length)}
		n++
	}
	w.start()
	w.check(binhttp.WriteResponseHead(w.c.bw, w.status, w.header, omitResponseField, extra[:n]...))
	w.cw.Reset(w.c.bw)
	w.headSent = true
	if w.req.Method == http.MethodHead {
		w.buf = nil
	}
	if len(w.buf) > 0 {
		_, err := w.cw.Write(w.buf)
		w.check(err)
		w.buf = nil
	}
}

// sniffData is what http.DetectContentType reads: the buffered bytes and,
// when they overflowed, the write that did.
func (w *hopResponseWriter) sniffData(more []byte) []byte {
	if len(more) == 0 || len(w.buf) >= sniffLen {
		return w.buf
	}
	return append(append(make([]byte, 0, len(w.buf)+len(more)), w.buf...), more...)
}

// finish ends the response after the handler returns: the head if it is
// still held, the content's end and the trailers.
func (w *hopResponseWriter) finish() {
	w.handlerDone = true
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.headSent {
		w.sendHead(nil)
	}
	w.check(w.cw.Close())
	w.check(binhttp.WriteTrailers(w.c.bw, w.trailers()))
	w.check(w.c.bw.Flush())
}

// trailers collects the values of the trailers the head declared and of
// keys set with http.TrailerPrefix.
func (w *hopResponseWriter) trailers() http.Header {
	var out http.Header
	add := func(k string, v []string) {
		if len(v) == 0 {
			return
		}
		if out == nil {
			out = http.Header{}
		}
		out[http.CanonicalHeaderKey(k)] = v
	}
	for _, v := range w.header.Values("Trailer") {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				add(name, w.header.Values(name))
			}
		}
	}
	for k, v := range w.header {
		if name, ok := strings.CutPrefix(k, http.TrailerPrefix); ok {
			add(name, v)
		}
	}
	return out
}

// bodyAllowedForStatus reports whether a response with status may have a
// body (RFC 9110 §6.4.1).
func bodyAllowedForStatus(status int) bool {
	switch {
	case status >= 100 && status <= 199:
		return false
	case status == http.StatusNoContent, status == http.StatusNotModified:
		return false
	}
	return true
}
