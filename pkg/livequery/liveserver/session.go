package liveserver

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
)

// Conn is a client connection's transport.
type Conn interface {
	// Send writes one frame. Only the session's writer calls it, one frame
	// at a time.
	Send(frame []byte) error
	Close() error
}

var sessionIDs atomic.Uint64

// Session is one client connection. The transport feeds it the client's
// frames with Receive and calls Close when the connection ends.
type Session struct {
	hub  *Hub
	id   uint64
	conn Conn
	out  chan []byte

	// Owned by the hub's loop.
	subs    map[string]*sub
	stopped bool

	stopOnce sync.Once
	done     chan struct{}
}

// Attach serves a new client connection.
func (h *Hub) Attach(ctx context.Context, conn Conn) *Session {
	s := &Session{hub: h, id: sessionIDs.Add(1), conn: conn, out: make(chan []byte, h.cfg.QueueFrames),
		subs: map[string]*sub{}, done: make(chan struct{})}
	h.do(ctx, func(context.Context) { h.sessions[s] = true })
	go s.write() //nolint:contextcheck // the writer lives as long as the session's queue, not the attaching request
	return s
}

// Receive hands one client frame to the hub.
func (s *Session) Receive(ctx context.Context, frame []byte) {
	data := append([]byte(nil), frame...)
	s.hub.do(ctx, func(loop context.Context) {
		if !s.stopped {
			s.hub.handle(loop, s, data)
		}
	})
}

// Close ends the session: its subscriptions leave the engine.
func (s *Session) Close(ctx context.Context) {
	s.hub.do(ctx, func(context.Context) { s.hub.detach(s) })
}

// Done is closed once the session's writer has stopped.
func (s *Session) Done() <-chan struct{} { return s.done }

func (h *Hub) detach(s *Session) {
	for _, sb := range s.subs {
		h.forget(sb, true)
	}
	delete(h.sessions, s)
	s.stop()
}

// live reports whether any subscription holds a result it keeps current.
func (s *Session) live() bool {
	for _, sb := range s.subs {
		if sb.ready && !sb.stale {
			return true
		}
	}
	return false
}

// send queues a frame. A client too slow to drain its queue is cut off:
// holding its frames would let one connection grow without bound.
func (s *Session) send(frame any) {
	if s.stopped {
		return
	}
	data, err := json.Marshal(frame)
	if err != nil {
		s.hub.cfg.Logger.Error("live frame encoding failed", "err", err)
		return
	}
	select {
	case s.out <- data:
	default:
		s.hub.cfg.Logger.Warn("live connection too slow, closing", "session", s.id)
		s.hub.cfg.Metrics.slowConsumer()
		s.hub.detach(s)
	}
}

// stop closes the outbound queue; the writer then closes the connection.
func (s *Session) stop() {
	s.stopped = true
	s.stopOnce.Do(func() { close(s.out) })
}

func (s *Session) write() {
	defer close(s.done)
	defer func() { _ = s.conn.Close() }()
	for data := range s.out {
		if err := s.conn.Send(data); err != nil {
			// The transport failed: drop what is queued and let the hub
			// forget the session.
			go s.Close(context.Background())
			for range s.out { //nolint:revive // drain until the hub stops the session
			}
			return
		}
	}
}
