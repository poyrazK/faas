package gateway

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// streamDeadlineRenewInterval rate-limits deadline renewals: a busy stream
// touches on every chunk, and one renewal per second keeps the window within
// a second of exact without a deadline syscall per write.
const streamDeadlineRenewInterval = time.Second

// streamDeadlines slides a listener's read and write deadlines while a
// long-lived response (SSE, chunked stream, gRPC stream, upgraded tunnel) is
// active. The public listener's ReadTimeout and WriteTimeout are the
// customer request envelope (about 25 minutes). An HTTP/1.1 server enforces
// WriteTimeout as an absolute write deadline, and its background reader
// cancels the request context once the whole-request read deadline passes,
// so every healthy stream was cut at the envelope however recently it made
// progress. Once started, each touch moves both deadlines to now plus the
// streaming idle window; a stream that goes quiet still ends at that window
// (and at the stream session's own idle timer). Until start, touch is a
// no-op, so ordinary requests keep the server's timeouts.
type streamDeadlines struct {
	rc     *http.ResponseController
	window time.Duration
	active atomic.Bool

	mu      sync.Mutex
	renewed time.Time
}

func newStreamDeadlines(w http.ResponseWriter, window time.Duration) *streamDeadlines {
	return &streamDeadlines{rc: http.NewResponseController(w), window: window}
}

// start marks the response long-lived and installs the first window.
func (d *streamDeadlines) start() {
	d.active.Store(true)
	d.renew(true)
}

// touch records stream activity. Safe for concurrent use: the request body
// is read on the transport's goroutine while the response is copied here.
func (d *streamDeadlines) touch() {
	if d.active.Load() {
		d.renew(false)
	}
}

func (d *streamDeadlines) renew(force bool) {
	now := time.Now()
	d.mu.Lock()
	if !force && now.Sub(d.renewed) < streamDeadlineRenewInterval {
		d.mu.Unlock()
		return
	}
	d.renewed = now
	d.mu.Unlock()
	deadline := now.Add(d.window)
	// Writers without deadline support keep the server's safety net.
	_ = d.rc.SetWriteDeadline(deadline)
	_ = d.rc.SetReadDeadline(deadline)
}
