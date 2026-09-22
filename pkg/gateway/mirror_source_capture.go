package gateway

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// mirrorSourceResult is the bounded source-side response snapshot consumed by
// the asynchronous mirror comparator. It contains response bytes, never the
// request payload.
type mirrorSourceResult struct {
	StatusCode int
	Body       []byte
	Latency    time.Duration
}

// mirrorSourceCapture safely hands the source response from statusRecorder to
// one or more mirror goroutines. The customer response remains on the normal
// path; mirror goroutines wait on done independently and are bounded by their
// own dispatch deadline.
type mirrorSourceCapture struct {
	mu          sync.Mutex
	startedAt   time.Time
	status      int
	body        []byte
	completedAt time.Time
	done        chan struct{}
	once        sync.Once
}

func newMirrorSourceCapture() *mirrorSourceCapture {
	return &mirrorSourceCapture{
		startedAt: time.Now(),
		body:      make([]byte, 0, min(4096, int(mirrorResponseBodyCap))),
		done:      make(chan struct{}),
	}
}

func (c *mirrorSourceCapture) writeHeader(status int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.status == 0 {
		c.status = status
	}
	c.mu.Unlock()
}

func (c *mirrorSourceCapture) write(p []byte) {
	if c == nil || len(p) == 0 {
		return
	}
	c.mu.Lock()
	if c.status == 0 {
		c.status = http.StatusOK
	}
	remaining := int(mirrorResponseBodyCap) - len(c.body)
	if remaining > 0 {
		c.body = append(c.body, p[:min(len(p), remaining)]...)
	}
	c.mu.Unlock()
}

func (c *mirrorSourceCapture) complete() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.mu.Lock()
		c.completedAt = time.Now()
		c.mu.Unlock()
		close(c.done)
	})
}

func (c *mirrorSourceCapture) wait(ctx context.Context) (mirrorSourceResult, bool) {
	if c == nil {
		return mirrorSourceResult{}, false
	}
	read := func() (mirrorSourceResult, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		body := append([]byte(nil), c.body...)
		completedAt := c.completedAt
		if completedAt.IsZero() {
			completedAt = time.Now()
		}
		return mirrorSourceResult{
			StatusCode: c.status,
			Body:       body,
			Latency:    completedAt.Sub(c.startedAt),
		}, true
	}
	select {
	case <-c.done:
		return read()
	default:
	}
	select {
	case <-c.done:
		return read()
	case <-ctx.Done():
		return mirrorSourceResult{}, false
	}
}
