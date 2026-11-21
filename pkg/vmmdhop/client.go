package vmmdhop

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ErrFallback reports that the hop cannot carry this call and nothing was
// sent: the caller uses gRPC instead.
var ErrFallback = errors.New("vmmdhop: use gRPC for this call")

// fallbackFor is how long a node that does not offer the hop is left on gRPC.
const fallbackFor = time.Minute

// maxIdlePerNode bounds the idle connections kept for one vmmd.
const maxIdlePerNode = 64

// Client keeps hop connections to one vmmd.
type Client struct {
	address string
	tls     *tls.Config
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
	// Result, when set, counts how each call was carried.
	Result func(result string)

	mu            sync.Mutex
	idle          []*clientConn
	fallbackUntil time.Time
	closed        bool
	now           func() time.Time
}

// NewClient dials address with tlsCfg, which must be the gateway's vmmd
// client config for this node (client certificate, vmmd leaf pinned to the
// node). The hop offers only ALPN.
func NewClient(address string, tlsCfg *tls.Config) (*Client, error) {
	if tlsCfg == nil {
		return nil, errors.New("vmmdhop: the hop needs the gateway's vmmd client TLS config")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, err
	}
	cfg := tlsCfg.Clone()
	cfg.NextProtos = []string{ALPN}
	d := &net.Dialer{Timeout: handshakeTimeout, KeepAlive: 30 * time.Second}
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

	// While the connection is idle a watcher blocks reading it: nothing may
	// arrive between exchanges, so any return but our own deadline means
	// vmmd closed it (or broke the protocol).
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
// ErrFallback means vmmd does not offer the hop or could not be reached;
// nothing was sent.
func (c *Client) get(ctx context.Context) (*clientConn, error) {
	c.mu.Lock()
	if c.closed || c.now().Before(c.fallbackUntil) {
		c.mu.Unlock()
		return nil, ErrFallback
	}
	for len(c.idle) > 0 {
		cc := c.idle[len(c.idle)-1]
		c.idle = c.idle[:len(c.idle)-1]
		fresh := c.now().Sub(cc.idleFrom) < clientIdleTimeout
		c.mu.Unlock()
		if fresh && cc.claim() {
			return cc, nil
		}
		_ = cc.conn.Close()
		c.mu.Lock()
	}
	c.mu.Unlock()

	raw, err := c.dial(ctx, "tcp", c.address)
	if err != nil {
		c.result("dial_error")
		return nil, ErrFallback
	}
	conn := tls.Client(raw, c.tls)
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	err = conn.HandshakeContext(hctx)
	cancel()
	if err == nil && conn.ConnectionState().NegotiatedProtocol != ALPN {
		err = errors.New("vmmdhop: vmmd did not select the hop protocol")
	}
	if err != nil {
		_ = raw.Close()
		if ctx.Err() == nil {
			// A vmmd without the hop refuses the protocol: stay on gRPC a while.
			c.mu.Lock()
			c.fallbackUntil = c.now().Add(fallbackFor)
			c.mu.Unlock()
		}
		c.result("fallback")
		return nil, ErrFallback
	}
	return &clientConn{conn: conn, r: bufio.NewReaderSize(conn, 32<<10), w: bufio.NewWriterSize(conn, 32<<10)}, nil
}

func (c *Client) put(cc *clientConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || len(c.idle) >= maxIdlePerNode {
		_ = cc.conn.Close()
		return
	}
	cc.idleFrom = c.now()
	cc.watch()
	c.idle = append(c.idle, cc)
}

// NewStream starts method on a hop connection, carrying the outgoing
// metadata in ctx as gRPC would. ErrFallback means the caller must use gRPC.
func NewStream[Req, Resp any](ctx context.Context, c *Client, method string) (grpc.BidiStreamingClient[Req, Resp], error) {
	cc, err := c.get(ctx)
	if err != nil {
		return nil, err
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	otel.GetTextMapPropagator().Inject(ctx, metadataCarrier(md))
	if err := writePreamble(cc.w, method, md); err != nil {
		_ = cc.conn.Close()
		return nil, ErrFallback
	}
	c.result("hop")
	s := &clientStream[Req, Resp]{c: c, cc: cc, ctx: ctx}
	s.stop = context.AfterFunc(ctx, func() { s.abort() })
	return s, nil
}

// clientStream is the grpc.BidiStreamingClient the gateway's forwarder uses.
// One goroutine may Send while another calls Recv, as with gRPC.
type clientStream[Req, Resp any] struct {
	c    *Client
	cc   *clientConn
	ctx  context.Context
	stop func() bool

	sendMu     sync.Mutex
	sendFailed bool
	closedSend bool

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
		_ = s.cc.conn.Close()
	}
}

func (s *clientStream[Req, Resp]) Send(m *Req) error { return s.SendMsg(m) }

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
	if s.sendFailed || s.closedSend {
		return io.EOF
	}
	if err := writeFrame(s.cc.w, frameMessage, b); err == nil {
		if err = s.cc.w.Flush(); err == nil {
			return nil
		}
	}
	s.sendFailed = true
	return io.EOF
}

// CloseSend ends the request side.
func (s *clientStream[Req, Resp]) CloseSend() error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.sendFailed || s.closedSend {
		return nil
	}
	if err := writeFrame(s.cc.w, frameCloseSend, nil); err == nil {
		if err = s.cc.w.Flush(); err == nil {
			s.closedSend = true
			return nil
		}
	}
	s.sendFailed = true
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
	s.mu.Lock()
	if s.finished {
		err := s.err
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	typ, payload, err := readFrame(s.cc.r, max(MaxMessageBytes, maxStatusBytes))
	if err != nil {
		return s.finish(s.transportErr(err), false)
	}
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
	s.sendMu.Lock()
	clean := statusRead && s.closedSend && !s.sendFailed && s.ctx.Err() == nil
	s.sendMu.Unlock()
	if clean {
		s.c.put(s.cc)
	} else {
		_ = s.cc.conn.Close()
	}
	return err
}

func (s *clientStream[Req, Resp]) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (s *clientStream[Req, Resp]) Trailer() metadata.MD         { return metadata.MD{} }
func (s *clientStream[Req, Resp]) Context() context.Context     { return s.ctx }
