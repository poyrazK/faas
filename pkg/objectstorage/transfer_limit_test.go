package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeTransferClock advances only when a transfer sleeps, so a test reads the
// budget's pacing without waiting for it.
type fakeTransferClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeTransferClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeTransferClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return nil
}

func newTestTransferLimiter(t *testing.T, bytesPerSecond int64) (*TransferLimiter, *fakeTransferClock) {
	t.Helper()
	clock := &fakeTransferClock{now: time.Unix(1_800_000_000, 0)}
	l := NewTransferLimiter(bytesPerSecond, "test_gateway")
	l.now, l.sleep = clock.Now, clock.Sleep
	l.limiter.SetLimitAt(clock.Now(), l.limiter.Limit())
	return l, clock
}

// adr: 20261031122757 — reads and writes share one budget, and a transfer
// over it is paced rather than refused.
func TestTransferLimiterPacesBothDirectionsOnOneBudget(t *testing.T) {
	const share = 4 << 20
	l, clock := newTestTransferLimiter(t, share)
	start := clock.Now()
	payload := bytes.Repeat([]byte("x"), 6<<20)
	for _, d := range []TransferDirection{TransferDownload, TransferUpload} {
		n, err := io.Copy(io.Discard, l.Reader(context.Background(), bytes.NewReader(payload), d))
		if err != nil || n != int64(len(payload)) {
			t.Fatalf("%s copied %d, %v; want every byte", d, n, err)
		}
	}
	// 12 MiB through a 4 MiB/s budget with a 1 MiB burst takes 2.75 s.
	want := time.Duration(float64(2*len(payload)-api.ObjectGatewayTransferBurstBytes) / share * float64(time.Second))
	if got := clock.Now().Sub(start); got < want-time.Millisecond || got > want+time.Millisecond {
		t.Fatalf("12 MiB took %s on the fake clock, want %s", got, want)
	}
	for _, d := range []string{"download", "upload"} {
		if got := testutil.ToFloat64(l.bytes.WithLabelValues(d)); got != float64(len(payload)) {
			t.Fatalf("%s bytes = %v, want %d", d, got, len(payload))
		}
	}
	throttled := testutil.ToFloat64(l.throttled.WithLabelValues("download")) + testutil.ToFloat64(l.throttled.WithLabelValues("upload"))
	if throttled < want.Seconds()-0.001 || throttled > want.Seconds()+0.001 {
		t.Fatalf("throttled seconds = %v, want %v", throttled, want.Seconds())
	}
}

func TestTransferLimiterCountsWallClockOnceForConcurrentWaiters(t *testing.T) {
	l, clock := newTestTransferLimiter(t, 1<<20)
	start := clock.Now()
	l.startWaiting(TransferDownload, start)
	l.startWaiting(TransferDownload, start)
	clock.now = start.Add(2 * time.Second)
	l.stopWaiting(TransferDownload)
	l.stopWaiting(TransferDownload)
	if got := testutil.ToFloat64(l.throttled.WithLabelValues("download")); got != 2 {
		t.Fatalf("two transfers waiting together for 2 s counted %v s, want 2", got)
	}
}

func TestTransferLimiterStopsWhenTheTransferEnds(t *testing.T) {
	l, _ := newTestTransferLimiter(t, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	r := l.ReadCloser(ctx, io.NopCloser(strings.NewReader(strings.Repeat("x", 3<<20))), TransferUpload)
	buf := make([]byte, 4<<20)
	if n, err := r.Read(buf); err != nil || n != api.ObjectGatewayTransferBurstBytes {
		t.Fatalf("first read = %d, %v; want one burst", n, err)
	}
	cancel()
	n, err := r.Read(buf)
	if !errors.Is(err, context.Canceled) || n == 0 {
		t.Fatalf("read after the transfer ended = %d, %v; want the bytes read and the context's error", n, err)
	}
	if waited := r.(interface{ Waited() time.Duration }).Waited(); waited <= 0 {
		t.Fatalf("Waited = %s, want the time spent at the budget", waited)
	}
}

func TestTransferLimiterZeroBudgetIsUnlimited(t *testing.T) {
	l := NewTransferLimiter(0, "test_gateway")
	if l != nil {
		t.Fatal("a zero budget built a limiter")
	}
	r := strings.NewReader("x")
	if got := l.Reader(context.Background(), r, TransferDownload); got != io.Reader(r) {
		t.Fatal("a nil limiter wrapped the reader")
	}
}
