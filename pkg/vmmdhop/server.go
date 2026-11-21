package vmmdhop

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ServerConfig is what vmmd serves the hop with.
type ServerConfig struct {
	// TLS is vmmd's gRPC server config: same certificate, client
	// verification and reload hook. The hop clones it and offers only ALPN.
	TLS *tls.Config
	// Service is the gRPC service implementation the handlers are called on.
	Service any
	// Interceptor wraps every exchange, as the gRPC server's stream
	// interceptor chain does. It must enforce the caller policy.
	Interceptor grpc.StreamServerInterceptor
	// Streams maps a full method name to its generated stream handler. Any
	// other method is refused before a handler runs.
	Streams map[string]grpc.StreamHandler
	Log     *slog.Logger
	// Conns, when set, counts accepted hop connections by result.
	Conns func(result string)
}

// Server serves hop connections.
type Server struct {
	cfg    ServerConfig
	tls    *tls.Config
	tracer trace.Tracer
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	idle     map[*tls.Conn]struct{} // connections waiting for an exchange
	draining bool                   // no new connection or exchange starts
}

// NewServer validates cfg. Serving starts with NewListener. The hop's
// connections carry ctx's values; they end through Listener.Close (drain) or
// Close, not when ctx is cancelled, as gRPC's graceful stop decides when its
// streams end.
func NewServer(ctx context.Context, cfg ServerConfig) (*Server, error) {
	if cfg.TLS == nil {
		return nil, errors.New("vmmdhop: the hop needs vmmd's server TLS config")
	}
	if cfg.TLS.ClientAuth != tls.RequireAndVerifyClientCert {
		return nil, errors.New("vmmdhop: the server TLS config must require and verify client certificates")
	}
	if cfg.Interceptor == nil || cfg.Service == nil || len(cfg.Streams) == 0 {
		return nil, errors.New("vmmdhop: service, interceptor and streams are required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &Server{cfg: cfg, tls: hopServerTLS(cfg.TLS), tracer: otel.Tracer("github.com/onebox-faas/faas/pkg/vmmdhop"), ctx: ctx, cancel: cancel, idle: map[*tls.Conn]struct{}{}}, nil
}

// hopServerTLS offers only ALPN, including on the config a reload hook
// returns, so certificate rotation and peer verification apply unchanged.
func hopServerTLS(base *tls.Config) *tls.Config {
	cfg := base.Clone()
	cfg.NextProtos = []string{ALPN}
	if reload := base.GetConfigForClient; reload != nil {
		cfg.GetConfigForClient = func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			c, err := reload(chi)
			if err != nil || c == nil {
				return c, err
			}
			c = c.Clone()
			c.NextProtos = []string{ALPN}
			return c, nil
		}
	}
	return cfg
}

func (s *Server) count(result string) {
	if s.cfg.Conns != nil {
		s.cfg.Conns(result)
	}
}

// Drain ends idle connections now and every other one after its exchange,
// as gRPC's graceful stop lets calls in flight finish.
func (s *Server) Drain() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draining = true
	for c := range s.idle {
		_ = c.SetReadDeadline(time.Now())
	}
}

// Wait returns once every hop connection has ended. Call it after Drain: a
// draining server starts no connection, so none is missed.
func (s *Server) Wait() { s.wg.Wait() }

// Close ends every hop connection, exchanges in flight included, and waits
// for them.
func (s *Server) Close() {
	s.Drain()
	s.cancel()
	s.wg.Wait()
}

// track counts a new connection for Wait, unless the server is draining.
func (s *Server) track() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return false
	}
	s.wg.Add(1)
	return true
}

func (s *Server) isDraining() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.draining
}

// awaitExchange marks conn idle until its next exchange starts. It reports
// false once the server is draining.
func (s *Server) awaitExchange(conn *tls.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return false
	}
	s.idle[conn] = struct{}{}
	_ = conn.SetReadDeadline(time.Now().Add(serverIdleTimeout))
	return true
}

func (s *Server) exchangeStarted(conn *tls.Conn) {
	s.mu.Lock()
	delete(s.idle, conn)
	s.mu.Unlock()
}

// Listener sits in front of vmmd's TCP listener. It peeks at each
// connection's ClientHello: one that offers ALPN goes to the hop, and every
// other one comes out of Accept with its bytes replayed, for the gRPC server
// to handshake as before.
type Listener struct {
	net.Listener
	hop    *Server
	conns  chan net.Conn
	errc   chan error
	done   chan struct{}
	closed atomic.Bool
}

// NewListener starts routing lis's connections.
func NewListener(ctx context.Context, lis net.Listener, hop *Server) *Listener {
	l := &Listener{Listener: lis, hop: hop, conns: make(chan net.Conn), errc: make(chan error, 1), done: make(chan struct{})}
	go l.acceptLoop(ctx)
	return l
}

// acceptLoop feeds route. A temporary accept error (EMFILE, ECONNABORTED)
// is retried with backoff, as grpc-go's own accept loop does; any other ends
// the loop and is returned by Accept.
func (l *Listener) acceptLoop(ctx context.Context) {
	var backoff time.Duration
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			if temporaryAcceptError(err) {
				backoff = min(max(2*backoff, 5*time.Millisecond), time.Second)
				select {
				case <-time.After(backoff):
					continue
				case <-l.done:
					return
				}
			}
			select {
			case l.errc <- err:
			case <-l.done:
			}
			return
		}
		backoff = 0
		go l.route(ctx, c)
	}
}

func temporaryAcceptError(err error) bool {
	var ne interface{ Temporary() bool }
	if errors.As(err, &ne) && ne.Temporary() {
		return true
	}
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}

func (l *Listener) route(ctx context.Context, c net.Conn) {
	protos, replay, peeked := peekClientHello(c)
	if peeked && slices.Contains(protos, ALPN) {
		l.hop.serveConn(ctx, replay)
		return
	}
	select {
	case l.conns <- replay:
	case <-l.done:
		_ = c.Close()
	}
}

// Accept returns the next connection for the gRPC server.
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.errc:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close stops accepting and drains the hop: idle connections end now and
// the others after their exchange.
func (l *Listener) Close() error {
	if !l.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(l.done)
	err := l.Listener.Close()
	l.hop.Drain()
	return err
}

// peekClientHello reads a TLS ClientHello without answering it and reports
// the ALPN protocols it offers. The returned connection replays every byte
// read, so a connection that is not the hop's reaches gRPC unchanged. A
// connection that is not TLS, or too slow, comes back unpeeked.
func peekClientHello(c net.Conn) ([]string, net.Conn, bool) {
	rec := &recordingConn{Conn: c}
	var protos []string
	peeked := false
	_ = c.SetReadDeadline(time.Now().Add(handshakeTimeout))
	_ = tls.Server(rec, &tls.Config{
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			protos, peeked = slices.Clone(chi.SupportedProtos), true
			return nil, errPeeked
		},
	}).Handshake()
	_ = c.SetReadDeadline(time.Time{})
	return protos, &replayConn{Conn: c, buf: rec.buf}, peeked
}

var errPeeked = errors.New("vmmdhop: ClientHello read")

// recordingConn keeps every byte read and discards writes, so the peek's
// TLS server neither consumes the ClientHello nor sends an alert.
type recordingConn struct {
	net.Conn
	buf []byte
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.buf = append(c.buf, p[:n]...)
	return n, err
}

func (c *recordingConn) Write(p []byte) (int, error) { return len(p), nil }

// replayConn returns buf before reading the connection.
type replayConn struct {
	net.Conn
	buf []byte
}

func (c *replayConn) Read(p []byte) (int, error) {
	if len(c.buf) > 0 {
		n := copy(p, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// serveConn handshakes a hop connection and serves its exchanges in turn.
func (s *Server) serveConn(parent context.Context, raw net.Conn) {
	defer func() { _ = raw.Close() }()
	if !s.track() {
		return
	}
	defer s.wg.Done()
	// The connection outlives vmmd's run context on purpose: shutdown drains it
	// through Listener.Close, as gRPC's GracefulStop lets streams finish.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent)) //nolint:contextcheck // see above
	defer cancel()
	stopServer := context.AfterFunc(s.ctx, cancel)
	defer stopServer()
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()

	conn := tls.Server(raw, s.tls)
	hctx, hcancel := context.WithTimeout(ctx, handshakeTimeout)
	err := conn.HandshakeContext(hctx)
	hcancel()
	if err != nil {
		s.count("handshake_error")
		s.cfg.Log.Debug("vmmdhop: handshake failed", "remote", raw.RemoteAddr().String(), "err", err)
		return
	}
	state := conn.ConnectionState()
	// Defense in depth: the config requires a verified client certificate,
	// and the caller policy reads it from VerifiedChains.
	if state.NegotiatedProtocol != ALPN || len(state.VerifiedChains) == 0 {
		s.count("refused")
		return
	}
	s.count("served")
	p := &peer.Peer{
		Addr:      raw.RemoteAddr(),
		LocalAddr: raw.LocalAddr(),
		AuthInfo: credentials.TLSInfo{
			State:          state,
			CommonAuthInfo: credentials.CommonAuthInfo{SecurityLevel: credentials.PrivacyAndIntegrity},
		},
	}
	hc := &serverConn{s: s, conn: conn, r: bufio.NewReaderSize(conn, 32<<10), w: bufio.NewWriterSize(conn, 32<<10), peer: p}
	for {
		if !s.awaitExchange(conn) {
			hc.goAway()
			return
		}
		method, md, err := readPreamble(hc.r)
		s.exchangeStarted(conn)
		if err != nil {
			switch {
			case errors.Is(err, os.ErrDeadlineExceeded) && ctx.Err() == nil:
				// Drain or idle timeout: nothing after the last exchange was
				// processed, so the client may send it again elsewhere.
				hc.goAway()
			case !errors.Is(err, io.EOF) && ctx.Err() == nil:
				s.cfg.Log.Debug("vmmdhop: bad preamble", "remote", raw.RemoteAddr().String(), "err", fmt.Errorf("read preamble: %w", err))
			}
			return
		}
		_ = conn.SetReadDeadline(time.Time{})
		if !hc.exchange(ctx, method, md) {
			return
		}
	}
}

// goAway tells the client this connection ends without reading anything more.
func (c *serverConn) goAway() {
	_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
	if writeFrame(c.w, frameGoAway, nil) == nil {
		_ = c.w.Flush()
	}
}

// serverConn is one hop connection.
type serverConn struct {
	s    *Server
	conn *tls.Conn
	r    *bufio.Reader
	w    *bufio.Writer
	peer *peer.Peer
}

type recvItem struct {
	msg []byte
	err error
}

// exchange serves one call. It reports whether the connection may carry
// another: only after the client closed its send side and nothing else
// arrived.
func (c *serverConn) exchange(parent context.Context, method string, md metadata.MD) bool {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ctx = peer.NewContext(ctx, c.peer)
	ctx = metadata.NewIncomingContext(ctx, md)
	ctx = otel.GetTextMapPropagator().Extract(ctx, metadataCarrier(md))
	ctx, span := c.s.tracer.Start(ctx, method, trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	st := &serverStream{c: c, recv: make(chan recvItem, 4), handlerDone: make(chan struct{})}
	ctx = grpc.NewContextWithServerTransportStream(ctx, transportStream(method))
	st.ctx = ctx

	// The reader feeds RecvMsg, then watches the connection after the
	// client's close-send: anything but our own deadline cancels the call.
	// It starts on the handler's first RecvMsg, which the caller-policy
	// interceptor allows only after authorizing the caller, so nothing a
	// refused caller sends is read (gRPC refuses on stream open too).
	var stopping atomic.Bool
	readerDone := make(chan bool, 1) // true: stopped cleanly after close-send
	st.startReader = func() {
		go func() {
			closeSendSeen := false
			for {
				typ, payload, err := readFrame(c.r, MaxMessageBytes)
				if err != nil {
					if closeSendSeen && stopping.Load() && errors.Is(err, os.ErrDeadlineExceeded) {
						readerDone <- true
						return
					}
					cancel()
					if !closeSendSeen {
						st.deliver(ctx, recvItem{err: err})
					}
					readerDone <- false
					return
				}
				switch {
				case typ == frameMessage && !closeSendSeen:
					if !st.deliver(ctx, recvItem{msg: payload}) {
						readerDone <- false
						return
					}
				case typ == frameCloseSend && !closeSendSeen && len(payload) == 0:
					closeSendSeen = true
					st.deliver(ctx, recvItem{err: io.EOF})
				default:
					// A frame the client may not send now: end the call.
					cancel()
					if !closeSendSeen {
						st.deliver(ctx, recvItem{err: errMalformed})
					}
					readerDone <- false
					return
				}
			}
		}()
	}

	handler, ok := c.s.cfg.Streams[method]
	var err error
	if !ok {
		err = status.Errorf(codes.Unimplemented, "vmmdhop: method %s is not served on the hop", method)
	} else {
		info := &grpc.StreamServerInfo{FullMethod: method, IsClientStream: true, IsServerStream: true}
		err = c.s.cfg.Interceptor(c.s.cfg.Service, st, info, handler)
	}
	close(st.handlerDone)
	appStatus, ok := status.FromError(err)
	if !ok {
		// As grpc-go's server does for a handler error that is not a status.
		appStatus = status.FromContextError(err)
	}
	if appStatus.Code() != codes.OK {
		span.SetStatus(otelcodes.Error, appStatus.Message())
	}

	// Stop the reader before answering: after a clean close-send it is only
	// watching, so an expired deadline ends it without consuming anything.
	// The status then tells the client whether the connection stays open, so
	// it never pools one this side is about to close.
	// A reader that never started read nothing after the preamble: the
	// client's frames are still unread, so the connection cannot be reused.
	// Claiming the flag here also keeps a late RecvMsg from starting it.
	clean := false
	if !st.readerStarted.CompareAndSwap(false, true) {
		stopping.Store(true)
		_ = c.conn.SetReadDeadline(time.Now())
		clean = <-readerDone
		_ = c.conn.SetReadDeadline(time.Time{})
	}
	// A draining server keeps no connection, and says so in the status so
	// the client does not pool it.
	reusable := clean && ctx.Err() == nil && !c.s.isDraining()
	return st.writeStatus(appStatus, reusable) == nil && reusable
}

// metadataCarrier lets the W3C propagator read trace context from metadata.
type metadataCarrier metadata.MD

func (m metadataCarrier) Get(key string) string {
	if v := metadata.MD(m).Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

func (m metadataCarrier) Set(key, value string) { metadata.MD(m).Set(key, value) }

func (m metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

var _ propagation.TextMapCarrier = metadataCarrier(nil)

// transportStream is the call's grpc.ServerTransportStream, so grpc.Method
// reports the method. The handlers send no header or trailer metadata, and
// the hop carries none.
type transportStream string

func (t transportStream) Method() string             { return string(t) }
func (transportStream) SetHeader(metadata.MD) error  { return nil }
func (transportStream) SendHeader(metadata.MD) error { return nil }
func (transportStream) SetTrailer(metadata.MD) error { return nil }

// serverStream is the grpc.ServerStream a handler sees.
type serverStream struct {
	c           *serverConn
	ctx         context.Context
	recv        chan recvItem
	handlerDone chan struct{}
	sendMu      sync.Mutex
	failed      bool

	// startReader launches the frame reader; readerStarted makes it run
	// once, on the first RecvMsg.
	startReader   func()
	readerStarted atomic.Bool
}

// deliver hands the handler a message or the end of the client's side. It
// gives up when the call ends or the handler has returned.
func (st *serverStream) deliver(ctx context.Context, item recvItem) bool {
	select {
	case st.recv <- item:
		return true
	case <-ctx.Done():
		return false
	case <-st.handlerDone:
		return false
	}
}

func (st *serverStream) Context() context.Context     { return st.ctx }
func (st *serverStream) SetHeader(metadata.MD) error  { return nil }
func (st *serverStream) SendHeader(metadata.MD) error { return nil }
func (st *serverStream) SetTrailer(metadata.MD)       {}

// RecvMsg returns the next message, io.EOF after the client's close-send, or
// the call's cancellation, as grpc-go's server stream does.
func (st *serverStream) RecvMsg(m any) error {
	if st.readerStarted.CompareAndSwap(false, true) {
		st.startReader()
	}
	select {
	case item := <-st.recv:
		if item.err != nil {
			if errors.Is(item.err, io.EOF) {
				return io.EOF
			}
			return status.Error(codes.Canceled, "vmmdhop: client connection ended")
		}
		msg, ok := m.(proto.Message)
		if !ok {
			return status.Errorf(codes.Internal, "vmmdhop: %T is not a protobuf message", m)
		}
		if err := proto.Unmarshal(item.msg, msg); err != nil {
			return status.Errorf(codes.Internal, "vmmdhop: decode message: %v", err)
		}
		return nil
	case <-st.ctx.Done():
		return status.FromContextError(st.ctx.Err()).Err()
	}
}

// SendMsg writes one message and flushes it.
func (st *serverStream) SendMsg(m any) error {
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
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if err := st.ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if st.failed {
		return status.Error(codes.Unavailable, "vmmdhop: connection failed")
	}
	if err := writeFrame(st.c.w, frameMessage, b); err == nil {
		err = st.c.w.Flush()
		if err == nil {
			return nil
		}
	}
	st.failed = true
	return status.Error(codes.Unavailable, "vmmdhop: write to gateway failed")
}

// writeStatus ends the exchange; keep says whether the connection stays open.
func (st *serverStream) writeStatus(s *status.Status, keep bool) error {
	b, err := proto.Marshal(s.Proto())
	if err != nil || len(b) > maxStatusBytes {
		b, _ = proto.Marshal(status.New(s.Code(), "vmmdhop: status too large to send").Proto())
	}
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if st.failed {
		return errors.New("vmmdhop: connection failed")
	}
	typ := byte(frameStatusClose)
	if keep {
		typ = frameStatus
	}
	if err := writeFrame(st.c.w, typ, b); err != nil {
		return err
	}
	return st.c.w.Flush()
}

// StreamHandlers returns the generated stream handlers of desc for the named
// methods, keyed by full method name, for ServerConfig.Streams. A name desc
// does not have is left out.
func StreamHandlers(desc *grpc.ServiceDesc, names ...string) map[string]grpc.StreamHandler {
	out := make(map[string]grpc.StreamHandler, len(names))
	for _, name := range names {
		for _, s := range desc.Streams {
			if s.StreamName == name {
				out["/"+desc.ServiceName+"/"+name] = s.Handler
			}
		}
	}
	return out
}
