package objectstorage

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"
)

// TransferDirection names which way object bytes move through a gateway.
type TransferDirection int

const (
	// TransferDownload is a read: provider to gateway to client.
	TransferDownload TransferDirection = iota
	// TransferUpload is a write: client to gateway to provider.
	TransferUpload
)

func (d TransferDirection) String() string {
	if d == TransferUpload {
		return "upload"
	}
	return "download"
}

// TransferLimiter holds a gateway's object bytes to its share of the node's
// port (ADR-20261031122757 §3). Each byte a gateway moves crosses the port
// once in each direction (in from the provider and out to the client for a
// read, the reverse for a write), so reads and writes draw on one budget and
// the port carries at most that budget each way. A transfer over it slows
// down instead of failing.
//
// The time transfers spend waiting is wall-clock time at the share: while
// any transfer of a direction waits, that direction's
// <prefix>_transfer_throttled_seconds_total advances once, however many
// transfers wait together.
type TransferLimiter struct {
	limiter   *rate.Limiter
	now       func() time.Time
	sleep     func(context.Context, time.Duration) error
	bytes     *prometheus.CounterVec
	throttled *prometheus.CounterVec
	limit     prometheus.Gauge

	mu      sync.Mutex
	waiting [2]int
	since   [2]time.Time
}

// NewTransferLimiter returns nil for a zero budget: the gateway is not
// limited. Its metrics are named <prefix>_transfer_*.
func NewTransferLimiter(bytesPerSecond int64, prefix string) *TransferLimiter {
	if bytesPerSecond <= 0 {
		return nil
	}
	l := &TransferLimiter{
		limiter:   rate.NewLimiter(rate.Limit(bytesPerSecond), api.ObjectGatewayTransferBurstBytes),
		now:       time.Now,
		sleep:     sleepContext,
		bytes:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_transfer_bytes_total", Help: "Object bytes moved through the gateway under its port share, by direction."}, []string{"direction"}),
		throttled: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_transfer_throttled_seconds_total", Help: "Wall-clock seconds during which at least one transfer of the direction waited for the gateway's port share."}, []string{"direction"}),
		limit:     prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_transfer_limit_bytes_per_second", Help: "The gateway's port share in bytes per second, shared by both directions."}),
	}
	l.limit.Set(float64(bytesPerSecond))
	for _, d := range []TransferDirection{TransferDownload, TransferUpload} {
		l.bytes.WithLabelValues(d.String())
		l.throttled.WithLabelValues(d.String())
	}
	return l
}

func (l *TransferLimiter) Collectors() []prometheus.Collector {
	return []prometheus.Collector{l.bytes, l.throttled, l.limit}
}

// Reader paces r to the budget. A nil limiter returns r unchanged. Reads stop
// with the context's error when it ends while a read waits.
func (l *TransferLimiter) Reader(ctx context.Context, r io.Reader, d TransferDirection) io.Reader {
	if l == nil || r == nil {
		return r
	}
	return &limitedReader{ctx: ctx, r: r, l: l, d: d}
}

// ReadCloser is Reader for a request body, which the server must still
// close. Its Waited reports the time the body spent waiting for the budget,
// so a client's progress bound does not count the gateway's own pacing.
func (l *TransferLimiter) ReadCloser(ctx context.Context, r io.ReadCloser, d TransferDirection) io.ReadCloser {
	if l == nil || r == nil {
		return r
	}
	return &limitedReadCloser{limitedReader: limitedReader{ctx: ctx, r: r, l: l, d: d}, Closer: r}
}

type limitedReadCloser struct {
	limitedReader
	io.Closer
}

type limitedReader struct {
	ctx    context.Context
	r      io.Reader
	l      *TransferLimiter
	d      TransferDirection
	waited time.Duration
}

// Waited is the time this reader spent waiting for the budget.
func (r *limitedReader) Waited() time.Duration { return r.waited }

func (r *limitedReader) Read(p []byte) (int, error) {
	if len(p) > api.ObjectGatewayTransferBurstBytes {
		p = p[:api.ObjectGatewayTransferBurstBytes]
	}
	n, err := r.r.Read(p)
	if n > 0 {
		waited, waitErr := r.l.wait(r.ctx, n, r.d)
		r.waited += waited
		if waitErr != nil {
			return n, waitErr
		}
	}
	return n, err
}

// wait takes n bytes from the budget after they were read, so the next read
// starts only once the budget has room for them.
func (l *TransferLimiter) wait(ctx context.Context, n int, d TransferDirection) (time.Duration, error) {
	l.bytes.WithLabelValues(d.String()).Add(float64(n))
	now := l.now()
	delay := l.limiter.ReserveN(now, n).DelayFrom(now)
	if delay <= 0 {
		return 0, nil
	}
	l.startWaiting(d, now)
	err := l.sleep(ctx, delay)
	l.stopWaiting(d)
	return delay, err
}

func (l *TransferLimiter) startWaiting(d TransferDirection, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.waiting[d] == 0 {
		l.since[d] = now
	}
	l.waiting[d]++
}

func (l *TransferLimiter) stopWaiting(d TransferDirection) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.waiting[d]--
	if l.waiting[d] == 0 {
		l.throttled.WithLabelValues(d.String()).Add(l.now().Sub(l.since[d]).Seconds())
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
