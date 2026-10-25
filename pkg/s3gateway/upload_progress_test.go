package s3gateway

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// trickle returns one byte per Read and advances the clock by step.
type trickle struct {
	now  *time.Time
	step time.Duration
	left int
}

func (t *trickle) Read(b []byte) (int, error) {
	if t.left == 0 {
		return 0, io.EOF
	}
	*t.now = t.now.Add(t.step)
	t.left--
	b[0] = 'x'
	return 1, nil
}

func (t *trickle) Close() error { return nil }

// A request body holds an upload slot while it is read, so a client
// trickling bytes is cut off once its average falls below the floor after the
// grace period, instead of keeping the slot for the whole transfer budget.
func TestProgressBodyEndsATrickle(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := newProgressBody(httptest.NewRecorder(), &trickle{now: &now, step: 10 * time.Second, left: 1000}, now.Add(api.ObjectTransferTimeout))
	body.started, body.now = now, func() time.Time { return now }
	_, err := io.ReadAll(body)
	if !errors.Is(err, errUploadTooSlow) {
		t.Fatalf("trickling body = %v, want errUploadTooSlow", err)
	}
	if elapsed := now.Sub(body.started); elapsed > api.ObjectUploadMinRateGrace+20*time.Second {
		t.Fatalf("trickle held the body for %s, want it cut soon after the %s grace", elapsed, api.ObjectUploadMinRateGrace)
	}

	// A body that keeps up is read to the end.
	fast := newProgressBody(httptest.NewRecorder(), io.NopCloser(strings.NewReader(strings.Repeat("x", 1<<20))), time.Time{})
	if got, err := io.ReadAll(fast); err != nil || len(got) != 1<<20 {
		t.Fatalf("fast body = %d bytes, %v", len(got), err)
	}
}
