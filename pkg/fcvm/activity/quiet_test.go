package activity

import (
	"testing"
	"time"
)

// TestQuiet_ReportsWhenTheLastRequestEnded — ADR-1151 measures idleness
// from the end of the last request, so Quiet reports the time inflight
// dropped to zero, not the time a request began.
func TestQuiet_ReportsWhenTheLastRequestEnded(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000, 0)
	c := New(func() time.Time { return now })

	if _, _, ok := c.Quiet("inst-a"); ok {
		t.Fatal("Quiet on an unseen instance returned ok=true")
	}
	c.Begin("inst-a")
	c.Begin("inst-a")
	now = now.Add(5 * time.Second)
	c.End("inst-a")
	if inflight, _, ok := c.Quiet("inst-a"); !ok || inflight != 1 {
		t.Fatalf("Quiet with one request left = (%d, %v), want (1, true)", inflight, ok)
	}
	now = now.Add(5 * time.Second)
	c.End("inst-a")
	inflight, since, ok := c.Quiet("inst-a")
	if !ok || inflight != 0 || !since.Equal(time.Unix(1_010, 0)) {
		t.Fatalf("Quiet after the last End = (%d, %v, %v), want (0, 1010s, true)", inflight, since, ok)
	}
	now = now.Add(5 * time.Second)
	c.End("inst-a") // a stray End must not move the quiet time
	if _, since, _ := c.Quiet("inst-a"); !since.Equal(time.Unix(1_010, 0)) {
		t.Fatalf("stray End moved the quiet time to %v", since)
	}
}
