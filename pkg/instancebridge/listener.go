package instancebridge

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// Split serves session connections accepted on ln with sessions and
// returns the listener HTTP is served on, which yields every other
// connection. The first bytes of each connection decide: a session starts
// with Hello, an H2C or HTTP/1 connection never does.
func Split(ctx context.Context, ln net.Listener, sessions *Sessions) net.Listener {
	l := &splitListener{Listener: ln, conns: make(chan net.Conn), done: make(chan struct{})}
	go l.accept(ctx, sessions)
	return l
}

type splitListener struct {
	net.Listener
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
	mu    sync.Mutex
	err   error
}

func (l *splitListener) accept(ctx context.Context, sessions *Sessions) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			var temp interface{ Temporary() bool }
			if errors.As(err, &temp) && temp.Temporary() {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			l.stop(err)
			return
		}
		go l.route(ctx, conn, sessions)
	}
}

// route peeks at a new connection and hands it to sessions or to HTTP.
func (l *splitListener) route(ctx context.Context, conn net.Conn, sessions *Sessions) {
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	r := bufio.NewReader(conn)
	peek, err := r.Peek(len(Hello))
	_ = conn.SetReadDeadline(time.Time{})
	if err == nil && IsHello(peek) {
		sessions.Serve(ctx, conn, r)
		return
	}
	select {
	case l.conns <- &peekedConn{Conn: conn, r: r}:
	case <-l.done:
		_ = conn.Close()
	}
}

func (l *splitListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		l.mu.Lock()
		defer l.mu.Unlock()
		return nil, l.err
	}
}

func (l *splitListener) Close() error {
	err := l.Listener.Close()
	l.stop(net.ErrClosed)
	return err
}

// stop ends Accept with err, or with the first error already recorded.
func (l *splitListener) stop(err error) {
	l.mu.Lock()
	if l.err == nil {
		l.err = err
	}
	l.mu.Unlock()
	l.once.Do(func() { close(l.done) })
}

// peekedConn replays the bytes route peeked before reading the connection.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// CloseWrite keeps HTTP/1 half-closes working through the wrapper.
func (c *peekedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
