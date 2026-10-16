package workerpool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"sync"
	"time"
)

// A shared worker serves several requests at once (ADR-1342). Its ready
// handshake advertises how many. The pool tags each request with an id,
// writes requests to the worker's stdin one whole line at a time, and a
// reader goroutine routes every response line to its request by the id the
// adapter echoes. A request the runner abandons is cancelled rather than
// killed with its worker; a worker that does not acknowledge the cancel in
// time is presumed wedged (a blocked event loop) and restarted.

const (
	// maxWorkerSlots bounds the concurrency a worker may advertise.
	maxWorkerSlots = 256
	// streamBufferMax bounds the response lines queued for one request whose
	// reader has fallen behind; past it the request fails and is cancelled.
	// A line reaching an empty queue is always accepted, so one large
	// complete response still fits.
	streamBufferMax = 16 << 20
)

// cancelAckGrace is how long a shared worker has to acknowledge a cancel.
var cancelAckGrace = 5 * time.Second

// requestLineMax bounds one request line sent to a shared worker. The Python
// adapter's line reader stops at 1 GiB, so a longer line could never be read;
// refusing it up front also keeps the line's size arithmetic in range.
var requestLineMax = 1 << 30

var errStreamOverflow = errors.New("workerpool: response buffer exceeded")

// mux is a shared worker's request table.
type mux struct {
	// writeSem serializes whole request lines on the worker's stdin.
	writeSem chan struct{}

	mu      sync.Mutex
	nextID  uint64
	streams map[string]*stream
	// cancels holds completion fences for cancellations still cleaning up.
	cancels map[string]chan struct{}
	// err is set once the worker's stdout has ended.
	err error
}

func newMux() *mux {
	return &mux{writeSem: make(chan struct{}, 1), streams: map[string]*stream{}, cancels: map[string]chan struct{}{}}
}

func (m *mux) register() (string, *stream) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := strconv.FormatUint(m.nextID, 10)
	s := newStream()
	if m.err != nil {
		s.fail(m.err)
	}
	m.streams[id] = s
	return id, s
}

func (m *mux) unregister(id string) {
	m.mu.Lock()
	delete(m.streams, id)
	m.mu.Unlock()
}

// route hands one response line to its request. Lines for a request that
// has already been closed, or with no id, are dropped.
func (m *mux) route(line []byte) {
	id, ack, ok := lineID(line)
	if !ok {
		return
	}
	m.mu.Lock()
	if ack {
		if done := m.cancels[id]; done != nil {
			delete(m.cancels, id)
			close(done)
		}
		m.mu.Unlock()
		return
	}
	s := m.streams[id]
	m.mu.Unlock()
	if s != nil {
		s.push(line)
	}
}

// fail ends every request on a worker whose stdout has ended.
func (m *mux) fail(err error) {
	m.mu.Lock()
	m.err = err
	streams := m.streams
	m.streams = map[string]*stream{}
	for id, done := range m.cancels {
		delete(m.cancels, id)
		close(done)
	}
	m.mu.Unlock()
	for _, s := range streams {
		s.fail(err)
	}
}

// expectAck registers a completion fence for id's cancellation. It reports false
// when the worker is already gone and there is nothing to cancel.
func (m *mux) expectAck(id string) (<-chan struct{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, false
	}
	done := make(chan struct{})
	m.cancels[id] = done
	return done, true
}

const idPrefix = `{"__faas_id":"`

// lineID reads the request id a response line carries, or the id a cancel
// acknowledgement names (ack). Adapters write the id first, so the common
// case is read without decoding the whole line.
func lineID(line []byte) (id string, ack, ok bool) {
	if rest, found := bytes.CutPrefix(line, []byte(idPrefix)); found {
		if end := bytes.IndexByte(rest, '"'); end >= 0 && bytes.IndexByte(rest[:end], '\\') < 0 {
			return string(rest[:end]), false, true
		}
	}
	var tag struct {
		ID        *string `json:"__faas_id"`
		Cancelled *string `json:"__faas_cancelled"`
	}
	if json.Unmarshal(line, &tag) != nil {
		return "", false, false
	}
	if tag.Cancelled != nil {
		return *tag.Cancelled, true, true
	}
	if tag.ID != nil {
		return *tag.ID, false, true
	}
	return "", false, false
}

// withID prefixes a JSON object request with the id the worker echoes.
func withID(payload []byte, id string) ([]byte, error) {
	if len(payload) < 2 || payload[0] != '{' {
		return nil, errors.New("workerpool: a shared worker takes JSON object requests")
	}
	if len(payload) > requestLineMax {
		return nil, fmt.Errorf("workerpool: a %d-byte request exceeds the %d-byte shared worker line limit", len(payload), requestLineMax)
	}
	// Check the complete allocation size, including the generated id. Keep
	// this valid even if the line limit or id representation changes later.
	const framing = len(idPrefix) + 3
	if len(payload) > math.MaxInt-framing || len(id) > math.MaxInt-framing-len(payload) {
		return nil, errors.New("workerpool: shared request framing exceeds addressable size")
	}
	out := make([]byte, 0, len(payload)+len(idPrefix)+len(id)+3)
	out = append(append(append(out, idPrefix...), id...), '"')
	if len(bytes.TrimSpace(payload[1:len(payload)-1])) > 0 {
		out = append(append(out, ','), payload[1:]...)
	} else {
		out = append(out, '}')
	}
	return append(out, '\n'), nil
}

// send writes one whole line to a shared worker. It gives up before writing
// once ctx ends. A write that fails or is cut short leaves the worker's
// input unframed, so broken asks the caller to retire the worker.
func (w *worker) send(ctx context.Context, line []byte) (broken bool, err error) {
	select {
	case w.mux.writeSem <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		_, err := w.stdin.Write(line)
		<-w.mux.writeSem
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return true, fmt.Errorf("workerpool: write request: %w", err)
		}
		return false, nil
	case <-ctx.Done():
		select {
		case err := <-done:
			if err == nil {
				return false, nil
			}
		default:
		}
		return true, ctx.Err()
	}
}

// demux routes a shared worker's stdout until it ends, then retires it.
func (p *pool) demux(w *worker) {
	for {
		line, err := w.stdout.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			w.mux.route(line)
		}
		if err != nil {
			w.mux.fail(err)
			p.retire(w)
			return
		}
	}
}

// cancel tells a shared worker to drop request id. It outlives the request's
// ctx, which has usually ended. A worker that cannot take the cancel, or does
// not finish cleanup within the acknowledgement budget, is retired so abandoned
// work cannot occupy a slot indefinitely.
func (p *pool) cancel(ctx context.Context, w *worker, id string, grace time.Duration) {
	done, ok := w.mux.expectAck(id)
	if !ok {
		return
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace)
	defer cancel()
	if _, err := w.send(sendCtx, []byte(`{"__faas_cancel":"`+id+`"}`+"\n")); err != nil {
		p.retire(w)
		return
	}
	select {
	case <-done:
	case <-sendCtx.Done():
		log.Printf("workerpool: worker %d did not finish cancelling a request within %s; restarting it", w.cmd.Process.Pid, grace)
		p.retire(w)
	}
}

// stream is one request's queue of response lines on a shared worker.
type stream struct {
	mu    sync.Mutex
	lines [][]byte
	size  int
	err   error
	ready chan struct{}
}

func newStream() *stream { return &stream{ready: make(chan struct{}, 1)} }

func (s *stream) signal() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *stream) push(line []byte) {
	s.mu.Lock()
	switch {
	case s.err != nil:
	case len(s.lines) > 0 && s.size+len(line) > streamBufferMax:
		s.err, s.lines, s.size = errStreamOverflow, nil, 0
	default:
		s.lines = append(s.lines, line)
		s.size += len(line)
	}
	s.mu.Unlock()
	s.signal()
}

func (s *stream) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.signal()
}

// next returns the request's next line, then the error that ended it.
func (s *stream) next(ctx context.Context) ([]byte, error) {
	for {
		s.mu.Lock()
		if len(s.lines) > 0 {
			line := s.lines[0]
			s.lines[0] = nil
			s.lines = s.lines[1:]
			s.size -= len(line)
			s.mu.Unlock()
			return line, nil
		}
		err := s.err
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.ready:
		}
	}
}
