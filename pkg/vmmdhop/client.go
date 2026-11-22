package vmmdhop

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrFallback reports that the hop cannot carry this call and nothing was
// sent: the caller uses gRPC instead.
var ErrFallback = errors.New("vmmdhop: use gRPC for this call")

const (
	// fallbackFor is how long a node whose vmmd does not offer the hop stays
	// on gRPC.
	fallbackFor = time.Minute
	// dialErrorFor is how long a node the hop could not reach stays on gRPC,
	// so each call does not pay a dial attempt first.
	dialErrorFor = 5 * time.Second
	// dialTimeout bounds the hop's TCP dial and TLS handshake; a slower node
	// is left to gRPC.
	dialTimeout = 2 * time.Second
	// maxIdlePerNode bounds the idle connections kept for one vmmd.
	maxIdlePerNode = api.VmmdForwardHopMaxIdlePerNode
	// maxReplayBytes bounds what a stream keeps to replay an exchange vmmd
	// turned away unprocessed (GOAWAY); a larger request is not replayed.
	maxReplayBytes = api.VmmdForwardHopMaxReplayBytes
)

// Results a Client reports.
const (
	ResultHop       = "hop"        // the hop carried the call
	ResultFallback  = "fallback"   // vmmd does not offer the hop; gRPC carried it
	ResultDialError = "dial_error" // the hop could not reach vmmd; gRPC carried it
	ResultRetried   = "retried"    // resent on a new connection after vmmd's GOAWAY
)

// noApplicationProtocol reports a handshake vmmd refused because it offers
// none of the client's ALPN protocols (TLS alert 120). crypto/tls returns
// the alert it received as an unexported type inside a *net.OpError, so the
// exported AlertError does not match it; its text is stable.
func noApplicationProtocol(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, tls.AlertError(120)) || strings.Contains(err.Error(), "tls: no application protocol")
}

// Client keeps hop connections to one vmmd.
type Client struct {
	address string
	tls     *tls.Config
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
	// Result, when set, counts how each call was carried (Result*). A call
	// turned to gRPC while a node is held there counts under the reason it
	// is held.
	Result func(result string)

	mu             sync.Mutex
	idle           []*clientConn
	fallbackUntil  time.Time
	fallbackReason string
	closed         bool
	now            func() time.Time
}

// NewClient dials address with tlsCfg, which must be the gateway's vmmd
// client config for this node (client certificate, vmmd leaf pinned to the
// node). The hop offers only ALPN.
func NewClient(address string, tlsCfg *tls.Config) (*Client, error) {
	if tlsCfg == nil {
		return nil, errors.New("vmmdhop: the hop needs the gateway's vmmd client TLS config")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, fmt.Errorf("vmmdhop: client address %q: %w", address, err)
	}
	cfg := tlsCfg.Clone()
	cfg.NextProtos = []string{ALPN}
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &Client{address: address, tls: cfg, dial: d.DialContext, now: time.Now}, nil
}

func (c *Client) result(r string) {
	if c.Result != nil {
		c.Result(r)
	}
}

// Close drops the idle connections; calls in flight finish.
func (c *Client) Close() {
	c.mu.Lock()
	idle := c.idle
	c.idle, c.closed = nil, true
	c.mu.Unlock()
	for _, cc := range idle {
		_ = cc.conn.Close()
	}
}

type clientConn struct {
	conn     *tls.Conn
	r        *bufio.Reader
	w        *bufio.Writer
	idleFrom time.Time

	// While the connection is idle a watcher blocks reading it: nothing but
	// vmmd's GOAWAY may arrive between exchanges, so any return but our own
	// deadline means vmmd closed it or is closing it, and the watcher closes
	// it so it holds no descriptor while it waits in the pool.
	watchDone chan struct{}
	stopping  atomic.Bool
	dead      atomic.Bool
}

// watch starts the idle watcher.
func (cc *clientConn) watch() {
	cc.watchDone = make(chan struct{})
	cc.stopping.Store(false)
	go func() {
		defer close(cc.watchDone)
		_, err := cc.r.Peek(1)
		if !cc.stopping.Load() || !errors.Is(err, os.ErrDeadlineExceeded) {
			cc.dead.Store(true)
			_ = cc.conn.Close()
		}
	}()
}

// claim stops the idle watcher without waiting on the network and reports
// whether the connection is still usable.
func (cc *clientConn) claim() bool {
	if cc.watchDone != nil {
		cc.stopping.Store(true)
		_ = cc.conn.SetReadDeadline(time.Now())
		<-cc.watchDone
		_ = cc.conn.SetReadDeadline(time.Time{})
		cc.watchDone = nil
	}
	return !cc.dead.Load() && cc.r.Buffered() == 0
}

// get returns an idle connection that is still open, or dials one.
// ErrFallback means the caller must use gRPC; nothing was sent.
func (c *Client) get(ctx context.Context) (*clientConn, bool, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.result(ResultFallback)
		return nil, false, ErrFallback
	}
	if c.now().Before(c.fallbackUntil) {
		reason := c.fallbackReason
		c.mu.Unlock()
		c.result(reason)
		return nil, false, ErrFallback
	}
	for len(c.idle) > 0 {
		cc := c.idle[len(c.idle)-1]
		c.idle = c.idle[:len(c.idle)-1]
		fresh := c.now().Sub(cc.idleFrom) < clientIdleTimeout
		c.mu.Unlock()
		if fresh && cc.claim() {
			return cc, true, nil
		}
		_ = cc.conn.Close()
		c.mu.Lock()
	}
	c.mu.Unlock()
	cc, err := c.dialConn(ctx)
	if err != nil {
		return nil, false, ErrFallback
	}
	return cc, false, nil
}

// dialConn opens a hop connection. A vmmd that does not offer the hop keeps
// the node on gRPC for fallbackFor; any other failure for dialErrorFor.
func (c *Client) dialConn(ctx context.Context) (*clientConn, error) {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	raw, err := c.dial(dctx, "tcp", c.address)
	if err != nil {
		c.noteFailure(ctx, ResultDialError, dialErrorFor)
		return nil, fmt.Errorf("vmmdhop: dial %s: %w", c.address, err)
	}
	conn := tls.Client(raw, c.tls)
	err = conn.HandshakeContext(dctx)
	noHop := noApplicationProtocol(err)
	if err == nil && conn.ConnectionState().NegotiatedProtocol != ALPN {
		err, noHop = errors.New("vmmd did not select the hop protocol"), true
	}
	if err != nil {
		_ = raw.Close()
		if noHop {
			c.noteFailure(ctx, ResultFallback, fallbackFor)
		} else {
			c.noteFailure(ctx, ResultDialError, dialErrorFor)
		}
		return nil, fmt.Errorf("vmmdhop: handshake with %s: %w", c.address, err)
	}
	return &clientConn{conn: conn, r: bufio.NewReaderSize(conn, 32<<10), w: bufio.NewWriterSize(conn, 32<<10)}, nil
}

// noteFailure keeps the node on gRPC for d, unless the caller gave up (its
// cancellation says nothing about the node).
func (c *Client) noteFailure(ctx context.Context, result string, d time.Duration) {
	c.result(result)
	if ctx.Err() != nil {
		return
	}
	c.mu.Lock()
	if until := c.now().Add(d); until.After(c.fallbackUntil) {
		c.fallbackUntil, c.fallbackReason = until, result
	}
	c.mu.Unlock()
}

// put returns a connection to the pool, first closing pooled ones that went
// stale or that vmmd closed, so the pool holds no dead descriptors however
// deep it is.
func (c *Client) put(cc *clientConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	kept := c.idle[:0]
	for _, old := range c.idle {
		if old.dead.Load() || now.Sub(old.idleFrom) >= clientIdleTimeout {
			_ = old.conn.Close()
			continue
		}
		kept = append(kept, old)
	}
	clear(c.idle[len(kept):])
	c.idle = kept
	if c.closed || len(c.idle) >= maxIdlePerNode {
		_ = cc.conn.Close()
		return
	}
	cc.idleFrom = now
	cc.watch()
	c.idle = append(c.idle, cc)
}

// NewStream starts method on a hop connection, carrying the outgoing
// metadata in ctx as gRPC would. ErrFallback means the caller must use gRPC.
func NewStream[Req, Resp any](ctx context.Context, c *Client, method string) (grpc.BidiStreamingClient[Req, Resp], error) {
	cc, reused, err := c.get(ctx)
	if err != nil {
		return nil, err
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	otel.GetTextMapPropagator().Inject(ctx, metadataCarrier(md))
	var preamble bytes.Buffer
	pw := bufio.NewWriter(&preamble)
	if err := writePreamble(pw, method, md); err != nil || pw.Flush() != nil {
		c.put(cc)
		return nil, ErrFallback
	}
	if _, err := cc.w.Write(preamble.Bytes()); err != nil {
		_ = cc.conn.Close()
		return nil, ErrFallback
	}
	c.result(ResultHop)
	s := &clientStream[Req, Resp]{c: c, ctx: ctx, reused: reused}
	s.cc.Store(cc)
	s.replay = append([]byte(nil), preamble.Bytes()...)
	s.stop = context.AfterFunc(ctx, func() { s.abort() })
	return s, nil
}

// clientStream is the grpc.BidiStreamingClient the gateway's forwarder uses.
// One goroutine may Send while another calls Recv, as with gRPC.
//
// A send can block on a full TCP window while vmmd is not reading (the app
// answered before reading the whole body). Recv, abort and finish therefore
// never wait for sendMu: the connection is an atomic pointer and the send
// state atomic flags, so the response is still read, and a cancel still
// closes the connection, which unblocks the send.
type clientStream[Req, Resp any] struct {
	c      *Client
	ctx    context.Context
	stop   func() bool
	reused bool

	cc atomic.Pointer[clientConn] // replaced only by resend, under sendMu

	sendMu     sync.Mutex // orders writes; held across a blocking write
	sendFailed atomic.Bool
	closedSend atomic.Bool
	// replay holds every byte sent until the first answer, so an exchange
	// vmmd turned away unprocessed (GOAWAY) can be sent again; nil once it
	// grew past maxReplayBytes. Guarded by sendMu.
	replay   []byte
	answered atomic.Bool

	mu       sync.Mutex
	finished bool
	err      error // the call's final error once finished; io.EOF for OK
}

// abort closes the connection when the caller cancels.
func (s *clientStream[Req, Resp]) abort() {
	s.mu.Lock()
	finished := s.finished
	s.mu.Unlock()
	if !finished {
		_ = s.cc.Load().conn.Close()
	}
}

func (s *clientStream[Req, Resp]) Send(m *Req) error { return s.SendMsg(m) }

// writeLocked writes one frame and flushes it, recording it for a replay.
// The caller holds sendMu.
func (s *clientStream[Req, Resp]) writeLocked(typ byte, payload []byte) error {
	if !s.answered.Load() && s.replay != nil {
		if len(s.replay)+len(payload)+16 > maxReplayBytes {
			s.replay = nil
		} else {
			var frame bytes.Buffer
			fw := bufio.NewWriter(&frame)
			_ = writeFrame(fw, typ, payload)
			_ = fw.Flush()
			s.replay = append(s.replay, frame.Bytes()...)
		}
	}
	cc := s.cc.Load()
	if err := writeFrame(cc.w, typ, payload); err != nil {
		return err
	}
	return cc.w.Flush()
}

// SendMsg writes and flushes one message. As with gRPC, a failed send
// returns io.EOF and Recv reports the call's status.
func (s *clientStream[Req, Resp]) SendMsg(m any) error {
	msg, ok := m.(proto.Message)
	if !ok {
		return status.Errorf(codes.Internal, "vmmdhop: %T is not a protobuf message", m)
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return status.Errorf(codes.Internal, "vmmdhop: encode message: %v", err)
	}
	if len(b) > MaxMessageBytes {
		return status.Errorf(codes.ResourceExhausted, "vmmdhop: %d-byte message over the %d-byte limit", len(b), MaxMessageBytes)
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.sendFailed.Load() || s.closedSend.Load() {
		return io.EOF
	}
	if err := s.writeLocked(frameMessage, b); err != nil {
		s.sendFailed.Store(true)
		return io.EOF
	}
	return nil
}

// CloseSend ends the request side.
func (s *clientStream[Req, Resp]) CloseSend() error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.sendFailed.Load() || s.closedSend.Load() {
		return nil
	}
	if err := s.writeLocked(frameCloseSend, nil); err != nil {
		s.sendFailed.Store(true)
		return nil
	}
	s.closedSend.Store(true)
	return nil
}

func (s *clientStream[Req, Resp]) Recv() (*Resp, error) {
	m := new(Resp)
	if err := s.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// RecvMsg reads the next message. At the end it returns io.EOF for an OK
// status or the status error, as gRPC does; a broken connection is
// Unavailable and a cancelled call Canceled.
func (s *clientStream[Req, Resp]) RecvMsg(m any) error {
	for {
		s.mu.Lock()
		if s.finished {
			err := s.err
			s.mu.Unlock()
			return err
		}
		s.mu.Unlock()
		cc := s.cc.Load()
		typ, payload, err := readFrame(cc.r, max(MaxMessageBytes, maxStatusBytes))
		if err != nil {
			return s.finish(s.transportErr(err), false)
		}
		if typ == frameGoAway && !s.answered.Load() {
			// vmmd turned this connection away before reading the exchange:
			// nothing was processed, so it can be sent again, as gRPC retries
			// a stream an HTTP/2 GOAWAY says was not processed.
			if s.resend(cc) {
				continue
			}
			return s.finish(status.Error(codes.Unavailable, "vmmdhop: vmmd closed the connection before the exchange"), false)
		}
		s.answered.Store(true)
		switch typ {
		case frameMessage:
			msg, ok := m.(proto.Message)
			if !ok {
				return s.finish(status.Errorf(codes.Internal, "vmmdhop: %T is not a protobuf message", m), false)
			}
			if err := proto.Unmarshal(payload, msg); err != nil {
				return s.finish(status.Errorf(codes.Internal, "vmmdhop: decode message: %v", err), false)
			}
			return nil
		case frameStatus, frameStatusClose:
			keep := typ == frameStatus
			if len(payload) > maxStatusBytes {
				return s.finish(status.Error(codes.Internal, "vmmdhop: status over its limit"), false)
			}
			st, err := decodeStatus(payload)
			if err != nil {
				return s.finish(status.Errorf(codes.Internal, "vmmdhop: decode status: %v", err), false)
			}
			if st.Code() == codes.OK {
				return s.finish(io.EOF, keep)
			}
			return s.finish(st.Err(), keep)
		default:
			return s.finish(status.Error(codes.Internal, "vmmdhop: unexpected frame from vmmd"), false)
		}
	}
}

// resend replays the exchange on a fresh connection after a GOAWAY on old.
// It reports false when the request was too large to keep, a send already
// failed (the caller has stopped sending), or no connection could be opened.
func (s *clientStream[Req, Resp]) resend(old *clientConn) bool {
	// vmmd closes the connection after its GOAWAY; closing it here too
	// unblocks a send waiting on it, so sendMu comes free.
	_ = old.conn.Close()
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.replay == nil || s.sendFailed.Load() || s.ctx.Err() != nil {
		return false
	}
	cc, err := s.c.dialConn(s.ctx)
	if err != nil {
		return false
	}
	if _, err := cc.w.Write(s.replay); err != nil || cc.w.Flush() != nil {
		_ = cc.conn.Close()
		return false
	}
	s.cc.Store(cc)
	if s.ctx.Err() != nil {
		// Cancelled while dialing: abort may have closed the old connection
		// only.
		_ = cc.conn.Close()
		return false
	}
	s.c.result(ResultRetried)
	return true
}

func (s *clientStream[Req, Resp]) transportErr(err error) error {
	if ctxErr := s.ctx.Err(); ctxErr != nil {
		return status.FromContextError(ctxErr).Err()
	}
	return status.Errorf(codes.Unavailable, "vmmdhop: connection to vmmd failed: %v", err)
}

// finish records the call's end and returns the connection to the pool
// after a clean exchange that vmmd keeps open, closing it otherwise.
func (s *clientStream[Req, Resp]) finish(err error, statusRead bool) error {
	s.mu.Lock()
	if s.finished {
		err = s.err
		s.mu.Unlock()
		return err
	}
	s.finished, s.err = true, err
	s.mu.Unlock()
	s.stop()
	cc := s.cc.Load()
	if statusRead && s.closedSend.Load() && !s.sendFailed.Load() && s.ctx.Err() == nil {
		s.c.put(cc)
	} else {
		_ = cc.conn.Close()
	}
	return err
}

func (s *clientStream[Req, Resp]) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (s *clientStream[Req, Resp]) Trailer() metadata.MD         { return metadata.MD{} }
func (s *clientStream[Req, Resp]) Context() context.Context     { return s.ctx }
