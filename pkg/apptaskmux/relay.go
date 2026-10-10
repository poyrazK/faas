package apptaskmux

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

const streamQueueDepth = 64

// endpoint pumps streams between local connections and the frame stream.
type endpoint struct {
	w       *Writer
	mu      sync.Mutex
	streams map[uint32]*stream
	wg      sync.WaitGroup
}

// stream is one relayed connection. in carries the peer's data to the local
// connection; it closes when the peer sends EOF or Close.
type stream struct {
	mu     sync.Mutex
	in     chan []byte
	closed bool
	conn   net.Conn
	abort  bool
}

func newEndpoint(out io.Writer) *endpoint {
	return &endpoint{w: NewWriter(out), streams: make(map[uint32]*stream)}
}

// start registers id and pumps it once connect returns a connection. Data
// for the stream queues while connect runs. The stream ends when the local
// side reached EOF and the peer sent EOF, or when either side aborts.
func (e *endpoint) start(id uint32, connect func() (net.Conn, error)) bool {
	e.mu.Lock()
	if _, exists := e.streams[id]; exists || len(e.streams) >= MaxStreams {
		e.mu.Unlock()
		return false
	}
	s := &stream{in: make(chan []byte, streamQueueDepth)}
	e.streams[id] = s
	e.mu.Unlock()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer e.forget(id, s)
		conn, err := connect()
		if err != nil {
			_ = e.w.Close(id, err.Error())
			s.closeInput()
			drain(s.in)
			return
		}
		if !s.attach(conn) {
			_ = conn.Close()
			drain(s.in)
			return
		}
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			failed := false
			for payload := range s.in {
				if failed {
					continue
				}
				if _, err := conn.Write(payload); err != nil {
					failed = true
				}
			}
			if closer, ok := conn.(interface{ CloseWrite() error }); ok {
				_ = closer.CloseWrite()
			}
		}()
		e.pumpLocal(id, s, conn)
		<-writerDone
		_ = conn.Close()
	}()
	return true
}

// pumpLocal forwards the local connection's data until EOF (half-close) or
// an error (abort).
func (e *endpoint) pumpLocal(id uint32, s *stream, conn net.Conn) {
	buf := make([]byte, MaxPayload)
	for {
		n, readErr := conn.Read(buf)
		if n > 0 {
			if err := e.w.Data(id, buf[:n]); err != nil {
				s.closeInput()
				return
			}
		}
		if errors.Is(readErr, io.EOF) {
			_ = e.w.EOF(id)
			return
		}
		if readErr != nil {
			if !s.aborted() {
				_ = e.w.Close(id, "connection reset")
			}
			s.closeInput()
			return
		}
	}
}

func drain(in <-chan []byte) {
	for range in {
	}
}

func (e *endpoint) forget(id uint32, s *stream) {
	e.mu.Lock()
	if e.streams[id] == s {
		delete(e.streams, id)
	}
	e.mu.Unlock()
}

// attach records the connection; false means the stream was aborted while
// connecting.
func (s *stream) attach(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.abort {
		return false
	}
	s.conn = conn
	return true
}

func (s *stream) aborted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.abort
}

func (s *stream) closeInput() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.in)
	}
}

// abortStream tears the stream down in both directions.
func (s *stream) abortStream() {
	s.mu.Lock()
	s.abort = true
	conn := s.conn
	if !s.closed {
		s.closed = true
		close(s.in)
	}
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// send queues payload unless the peer's direction has ended. The queue is
// always drained, so holding the lock while it is full cannot deadlock.
func (s *stream) send(payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.in <- payload
	}
}

// dispatch applies one Data, EOF or Close frame from the peer.
func (e *endpoint) dispatch(frame Frame) {
	e.mu.Lock()
	s := e.streams[frame.Stream]
	e.mu.Unlock()
	if s == nil {
		return
	}
	switch frame.Type {
	case FrameData:
		s.send(frame.Payload)
	case FrameEOF:
		s.closeInput()
	case FrameClose:
		s.abortStream()
	}
}

func (e *endpoint) shutdown() {
	e.mu.Lock()
	streams := make([]*stream, 0, len(e.streams))
	for _, s := range e.streams {
		streams = append(streams, s)
	}
	e.mu.Unlock()
	for _, s := range streams {
		s.abortStream()
	}
}

// Serve is the guest side: every Open frame dials a new connection with
// dial. It returns when in ends; open streams are then closed.
func Serve(ctx context.Context, in io.Reader, out io.Writer, dial func(context.Context) (net.Conn, error)) error {
	e := newEndpoint(out)
	defer e.wg.Wait()
	defer e.shutdown()
	r := NewReader(in)
	for {
		frame, err := r.Next()
		if errors.Is(err, io.EOF) || ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if frame.Type == FrameOpen {
			if !e.start(frame.Stream, func() (net.Conn, error) { return dial(ctx) }) {
				_ = e.w.Close(frame.Stream, "stream rejected")
			}
			continue
		}
		e.dispatch(frame)
	}
}

// Forward is the client side: every connection accepted on ln becomes a
// stream. It returns when the peer's frame stream ends or ctx is done.
func Forward(ctx context.Context, ln net.Listener, toPeer io.Writer, fromPeer io.Reader) error {
	e := newEndpoint(toPeer)
	defer e.wg.Wait()
	defer e.shutdown()
	var next atomic.Uint32
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			id := next.Add(1)
			if !e.start(id, func() (net.Conn, error) {
				if err := e.w.Open(id); err != nil {
					return nil, err
				}
				return conn, nil
			}) {
				_ = conn.Close()
			}
		}
	}()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	r := NewReader(fromPeer)
	for {
		frame, err := r.Next()
		if err != nil {
			_ = ln.Close()
			<-acceptDone
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if frame.Type == FrameOpen {
			continue
		}
		e.dispatch(frame)
	}
}
