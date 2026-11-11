package gateway

// adr: 20261110081523 — the compute edge sends the requests it routes to
// its own node's gatewayd-internal in binary HTTP.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/binhttp"
)

// Reasons a request that should have taken the binary hop went over
// HTTP/1.1 instead, the label values of the edge's fallback counter.
const (
	// HopFallbackNoSocket: the hop socket is missing or nothing listens on
	// it, as beside a gatewayd-internal from before the hop.
	HopFallbackNoSocket = "no_socket"
	// HopFallbackDialError: the dial failed some other way.
	HopFallbackDialError = "dial_error"
)

// HopFallbackReasons lists the fallback reasons, so a counter can start
// each at zero.
var HopFallbackReasons = []string{HopFallbackNoSocket, HopFallbackDialError}

// binaryHopMaxIdle bounds the idle connections the edge keeps to its own
// gatewayd-internal.
const binaryHopMaxIdle = 64

// hopWriteSettle is how long a response that ended before its request's body
// writer waits for that writer, to keep the connection for the next request.
const hopWriteSettle = 5 * time.Millisecond

// defaultUserAgent is what net/http's client sends when a request has no
// User-Agent field at all; the binary hop sends the same, so gatewayd-internal
// sees what the HTTP/1.1 hop would show it.
const defaultUserAgent = "Go-http-client/1.1"

// binaryHopTransport sends one request at a time over each connection to
// this node's gatewayd-internal and keeps idle connections for reuse.
type binaryHopTransport struct {
	dialTimeout    time.Duration
	idleTimeout    time.Duration
	maxHeaderBytes int

	mu   sync.Mutex
	idle []*hopClientConn
}

func newBinaryHopTransport(dialTimeout time.Duration) *binaryHopTransport {
	return &binaryHopTransport{
		dialTimeout:    dialTimeout,
		idleTimeout:    upstreamIdleConnTimeout,
		maxHeaderBytes: int(api.DefaultMaxHeaderBytes),
	}
}

type hopClientConn struct {
	conn   net.Conn
	br     *bufio.Reader
	bw     *bufio.Writer
	idleAt time.Time
	reused bool
}

// hopDialError marks a failure to open a connection to the hop socket:
// nothing of the request was sent, so it can go over HTTP/1.1 instead.
type hopDialError struct {
	err      error
	noSocket bool
}

func (e *hopDialError) Error() string { return "binary hop: dial: " + e.err.Error() }
func (e *hopDialError) Unwrap() error { return e.err }

// reason is the fallback counter's label for e.
func (e *hopDialError) reason() string {
	if e.noSocket {
		return HopFallbackNoSocket
	}
	return HopFallbackDialError
}

// roundTrip sends req over a connection from dial or the idle pool. A
// request with no body that fails on a reused connection before any of its
// response arrived is sent once more on a new connection: the server may
// have closed the idle connection as the request went out.
func (t *binaryHopTransport) roundTrip(req *http.Request, dial func(context.Context) (net.Conn, error)) (*http.Response, error) {
	noContent := req.Body == nil || req.Body == http.NoBody
	for attempt := 0; ; attempt++ {
		cc, err := t.conn(req.Context(), dial, attempt > 0)
		if err != nil {
			return nil, err
		}
		resp, err := t.send(cc, req, noContent)
		if err == nil {
			return resp, nil
		}
		var retry *hopRetryableError
		if attempt == 0 && cc.reused && noContent && idempotent(req.Method) && errors.As(err, &retry) && req.Context().Err() == nil {
			continue
		}
		return nil, err
	}
}

// idempotent reports whether a request with method may be sent again, as
// net/http's client decides for a connection the server closed.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// hopRetryableError is a failure on a connection before any response byte
// arrived.
type hopRetryableError struct{ err error }

func (e *hopRetryableError) Error() string { return e.err.Error() }
func (e *hopRetryableError) Unwrap() error { return e.err }

// conn returns an idle connection or a new one. fresh skips the pool.
func (t *binaryHopTransport) conn(ctx context.Context, dial func(context.Context) (net.Conn, error), fresh bool) (*hopClientConn, error) {
	if !fresh {
		if cc := t.takeIdle(); cc != nil {
			return cc, nil
		}
	}
	c, err := dialWithTimeout(ctx, upstreamDialer(dial), t.dialTimeout)
	if err != nil {
		return nil, &hopDialError{err: err, noSocket: errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)}
	}
	return &hopClientConn{conn: c, br: bufio.NewReaderSize(c, hopBufferSize), bw: bufio.NewWriterSize(c, hopBufferSize)}, nil
}

func (t *binaryHopTransport) takeIdle() *hopClientConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.idle) > 0 {
		cc := t.idle[len(t.idle)-1]
		t.idle = t.idle[:len(t.idle)-1]
		if time.Since(cc.idleAt) < t.idleTimeout {
			cc.reused = true
			return cc
		}
		_ = cc.conn.Close()
	}
	return nil
}

func (t *binaryHopTransport) putIdle(cc *hopClientConn) {
	cc.idleAt = time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.idle) >= binaryHopMaxIdle {
		_ = t.idle[0].conn.Close()
		t.idle = t.idle[1:]
	}
	t.idle = append(t.idle, cc)
}

// closeIdle closes every idle connection.
func (t *binaryHopTransport) closeIdle() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, cc := range t.idle {
		_ = cc.conn.Close()
	}
	t.idle = nil
}

// send writes req and reads its response head on cc. The body streams on
// another goroutine, so a response may begin before the request ends.
func (t *binaryHopTransport) send(cc *hopClientConn, req *http.Request, noContent bool) (*http.Response, error) {
	ctx := req.Context()
	stop := context.AfterFunc(ctx, func() { _ = cc.conn.Close() })
	fail := func(err error) (*http.Response, error) {
		stop()
		_ = cc.conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	head := binhttp.RequestHead{
		Method:    req.Method,
		Scheme:    "http",
		Authority: req.Host,
		Path:      req.URL.RequestURI(),
		Header:    hopRequestHeader(req, noContent),
		NoContent: noContent,
	}
	if head.Authority == "" {
		head.Authority = req.URL.Host
	}
	// The head goes out now: a streamed body may wait for the response.
	if err := binhttp.WriteRequestHead(cc.bw, head); err != nil {
		return fail(&hopRetryableError{err})
	}
	if err := cc.bw.Flush(); err != nil {
		return fail(&hopRetryableError{err})
	}
	// writeDone stays nil for a request with no content: it is all sent.
	var writeDone chan error
	if !noContent {
		writeDone = make(chan error, 1)
		go func() { writeDone <- writeHopBody(cc.bw, req) }()
	}

	if err := binhttp.ReadResponseStart(cc.br); err != nil {
		return fail(&hopRetryableError{err})
	}
	for {
		status, header, err := binhttp.ReadResponseHead(cc.br, t.maxHeaderBytes)
		if err != nil {
			return fail(err)
		}
		if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
			if trace := httptrace.ContextClientTrace(ctx); trace != nil && trace.Got1xxResponse != nil && status != http.StatusContinue {
				if err := trace.Got1xxResponse(status, textproto.MIMEHeader(header)); err != nil {
					return fail(err)
				}
			}
			continue
		}
		resp := &http.Response{
			Status:        strconv.Itoa(status) + " " + http.StatusText(status),
			StatusCode:    status,
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        header,
			Request:       req,
			ContentLength: hopResponseLength(req, status, header),
			Close:         strings.EqualFold(strings.TrimSpace(header.Get("Connection")), "close"),
			Trailer:       declaredTrailers(header),
		}
		body := &hopResponseBody{t: t, cc: cc, resp: resp, writeDone: writeDone, stop: stop}
		body.cr.Reset(cc.br)
		resp.Body = body
		return resp, nil
	}
}

// hopRequestHeader is the header to send: the request's own, with the
// fields net/http's client would add.
func hopRequestHeader(req *http.Request, noContent bool) http.Header {
	_, hasUA := req.Header["User-Agent"]
	needLength := !noContent && req.ContentLength > 0 && req.Header.Get("Content-Length") == ""
	if hasUA && !needLength && len(req.Trailer) == 0 {
		return req.Header
	}
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	if !hasUA {
		h.Set("User-Agent", defaultUserAgent)
	}
	if needLength {
		h.Set("Content-Length", strconv.FormatInt(req.ContentLength, 10))
	}
	if len(req.Trailer) > 0 && h.Get("Trailer") == "" {
		names := make([]string, 0, len(req.Trailer))
		for k := range req.Trailer {
			names = append(names, k)
		}
		h.Set("Trailer", strings.Join(names, ", "))
	}
	return h
}

// writeHopBody streams req's body as chunks, then its trailers, flushing
// each read so a streaming body reaches the server as it arrives.
func writeHopBody(bw *bufio.Writer, req *http.Request) error {
	defer func() { _ = req.Body.Close() }()
	cw := binhttp.NewChunkWriter(bw)
	buf := make([]byte, hopBufferSize)
	for {
		n, err := req.Body.Read(buf)
		if n > 0 {
			if _, werr := cw.Write(buf[:n]); werr != nil {
				return werr
			}
			if werr := bw.Flush(); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("binary hop: read request body: %w", err)
		}
	}
	if err := cw.Close(); err != nil {
		return err
	}
	if err := binhttp.WriteTrailers(bw, req.Trailer); err != nil {
		return err
	}
	return bw.Flush()
}

// hopResponseLength is the response's declared length, as net/http's
// client reports it.
func hopResponseLength(req *http.Request, status int, header http.Header) int64 {
	if !bodyAllowedForStatus(status) {
		return 0
	}
	if v := header.Get("Content-Length"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return -1
}

// declaredTrailers holds a key for each trailer the head declared, filled in
// once the body ends, as net/http's HTTP/1.1 client does.
func declaredTrailers(header http.Header) http.Header {
	var out http.Header
	for _, v := range header.Values("Trailer") {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				if out == nil {
					out = http.Header{}
				}
				out[http.CanonicalHeaderKey(name)] = nil
			}
		}
	}
	return out
}

// hopResponseBody reads a response's chunks and trailers, then hands the
// connection back when the request was written in full and the server
// keeps the connection.
type hopResponseBody struct {
	t         *binaryHopTransport
	cc        *hopClientConn
	resp      *http.Response
	cr        binhttp.ChunkReader
	writeDone chan error
	stop      func() bool

	mu     sync.Mutex
	closed bool
	ended  bool
	err    error
}

func (b *hopResponseBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, errors.New("binary hop: read on a closed body")
	}
	if b.err != nil {
		return 0, b.err
	}
	if b.ended {
		return 0, io.EOF
	}
	n, err := b.cr.Read(p)
	if errors.Is(err, io.EOF) {
		trailer, terr := binhttp.ReadTrailers(b.cc.br, b.t.maxHeaderBytes)
		if terr != nil {
			b.err = terr
			b.abort()
			return n, terr
		}
		for k, v := range trailer {
			if b.resp.Trailer == nil {
				b.resp.Trailer = http.Header{}
			}
			b.resp.Trailer[k] = v
		}
		b.ended = true
		b.release()
		return n, io.EOF
	}
	if err != nil {
		if ctxErr := b.resp.Request.Context().Err(); ctxErr != nil {
			err = ctxErr
		}
		b.err = err
		b.abort()
	}
	return n, err
}

func (b *hopResponseBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if !b.ended && b.err == nil {
		b.abort()
	}
	return nil
}

// release returns the connection to the pool when the request has been
// written in full and the server did not ask to close it.
func (b *hopResponseBody) release() {
	if !b.stop() {
		return // the request's context ended and closed the connection
	}
	reusable := !b.resp.Close && b.cc.br.Buffered() == 0
	if b.writeDone == nil {
		b.handBack(reusable)
		return
	}
	select {
	case err := <-b.writeDone:
		b.handBack(reusable && err == nil)
		return
	default:
	}
	// The response ended before the body writer reported. Usually the
	// server read the whole request first and the writer is returning, so
	// wait a moment and keep the connection for the next request. Past
	// that, the server answered early: the connection is reusable once the
	// rest of the request is sent, and if the server stopped reading, the
	// write fails and the connection closes.
	settle := time.NewTimer(hopWriteSettle)
	defer settle.Stop()
	select {
	case err := <-b.writeDone:
		b.handBack(reusable && err == nil)
	case <-settle.C:
		go func() { b.handBack(reusable && <-b.writeDone == nil) }()
	}
}

func (b *hopResponseBody) handBack(reusable bool) {
	if reusable {
		b.t.putIdle(b.cc)
		return
	}
	_ = b.cc.conn.Close()
}

// abort closes the connection mid-message.
func (b *hopResponseBody) abort() {
	b.stop()
	_ = b.cc.conn.Close()
}
