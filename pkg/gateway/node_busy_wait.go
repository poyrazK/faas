package gateway

import (
	"bytes"
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// A node refuses a request before guest execution when its instance already
// holds its concurrency_per_vm (vmmdgrpc instanceBusy). Every gateway tracks
// only its own in-flight count, so with several gateways or a burst during
// scale-out the instance is full before any one gateway's queue engages, and
// the refusal used to reach the client at once as 429 concurrency_throttled,
// although the app's overflow policy was "queue" (prod 2026-11-19: 20 of 260
// concurrent requests on 3 warm instances × 80; 140 of 450 while scaling
// 1→3). The gateway now holds such a refusal and re-forwards within the app's
// warm-saturation queue wait, preferring another instance, before relaying it.

const (
	nodeBusyFirstBackoff = 25 * time.Millisecond
	nodeBusyMaxBackoff   = 200 * time.Millisecond
)

type nodeBusySignal struct{ busy atomic.Bool }

type nodeBusySignalKey struct{}

// markNodeBusy records that the node refused the request for capacity. It is
// called before the refusal is written, so the writer can hold it.
func markNodeBusy(ctx context.Context) {
	if s, ok := ctx.Value(nodeBusySignalKey{}).(*nodeBusySignal); ok {
		s.busy.Store(true)
	}
}

// nodeBusyWriter passes every response straight through except a refusal the
// node marked busy, which it holds so the caller can replace it.
type nodeBusyWriter struct {
	dst    http.ResponseWriter
	signal *nodeBusySignal
	header http.Header
	status int
	held   bool
	body   bytes.Buffer
}

func newNodeBusyWriter(dst http.ResponseWriter, signal *nodeBusySignal) *nodeBusyWriter {
	return &nodeBusyWriter{dst: dst, signal: signal, header: dst.Header().Clone()}
}

func (w *nodeBusyWriter) Header() http.Header {
	if w.status != 0 && !w.held {
		return w.dst.Header()
	}
	return w.header
}

func (w *nodeBusyWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if isInformationalStatus(status) {
		w.copyHeader()
		w.dst.WriteHeader(status)
		return
	}
	w.status = status
	if status == http.StatusTooManyRequests && w.signal.busy.Load() {
		w.held = true
		return
	}
	w.moveHeader()
	w.dst.WriteHeader(status)
}

func (w *nodeBusyWriter) copyHeader() {
	for key, values := range w.header {
		w.dst.Header()[key] = append([]string(nil), values...)
	}
}

// moveHeader hands the buffered value slices to dst instead of copying them
// (a slice per header per request). Only for the final, non-held header:
// from then on Header() returns dst's map and w.header is never read or
// written again. The buffer came from Header.Clone, which gives each key a
// capacity-limited window, so a later Add on dst reallocates rather than
// writing into another key's values.
func (w *nodeBusyWriter) moveHeader() {
	dst := w.dst.Header()
	for key, values := range w.header {
		dst[key] = values
	}
}

func (w *nodeBusyWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.held {
		return w.body.Write(p)
	}
	return w.dst.Write(p)
}

func (w *nodeBusyWriter) Flush() {
	if w.held {
		return
	}
	if flusher, ok := w.dst.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *nodeBusyWriter) Unwrap() http.ResponseWriter { return w.dst }

// release writes the held refusal unchanged.
func (w *nodeBusyWriter) release() {
	if !w.held {
		return
	}
	w.held = false
	w.copyHeader()
	w.dst.WriteHeader(w.status)
	if w.body.Len() > 0 {
		_, _ = w.dst.Write(w.body.Bytes())
	}
}

// waitOutNodeBusy wraps forward so a node capacity refusal is retried within
// wait. repick may return another instance; it never evicts the busy one.
func waitOutNodeBusy(wait time.Duration, forward retryAttempt, repick func(prev Target) (Target, bool)) retryAttempt {
	return func(dst http.ResponseWriter, req *http.Request, selected Target) {
		replayable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
		if wait <= 0 || !replayable {
			forward(dst, req, selected)
			return
		}
		deadline := time.Now().Add(wait)
		backoff := nodeBusyFirstBackoff
		for {
			attempt, err := replayRequest(req)
			if err != nil {
				forward(dst, req, selected)
				return
			}
			signal := &nodeBusySignal{}
			writer := newNodeBusyWriter(dst, signal)
			forward(writer, attempt.WithContext(context.WithValue(req.Context(), nodeBusySignalKey{}, signal)), selected)
			if !writer.held {
				return
			}
			if time.Now().Add(backoff).After(deadline) {
				writer.release()
				return
			}
			timer := time.NewTimer(backoff)
			select {
			case <-req.Context().Done():
				timer.Stop()
				writer.release()
				return
			case <-timer.C:
			}
			backoff = min(backoff*2, nodeBusyMaxBackoff)
			if next, ok := repick(selected); ok {
				selected = next
			}
		}
	}
}
