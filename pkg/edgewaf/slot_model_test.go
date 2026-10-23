package edgewaf

import (
	"container/heap"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// slotModel is a discrete-event model of the in-path check pool (ADR-831
// amendment 11): Poisson arrivals, `slots` checks at once, check times
// lognormal with the @grx production median (1.0 ms) and p95 (1.7 ms) from
// amendment 10, and a request that finds every slot busy waiting FIFO up to
// `wait` before it is skipped. It returns the skipped fraction. It cannot
// model CPU contention, which lengthens checks as more overlap, so it is
// optimistic where the node saturates (about 1000 req/s on n2-standard-4).
func slotModel(rate float64, slots int, wait time.Duration, seconds int, seed int64) float64 {
	rng := rand.New(rand.NewSource(seed))
	sigma := math.Log(1.7) / 1.6449
	check := func() float64 { return math.Exp(sigma * rng.NormFloat64()) } // ms, median 1.0
	waitMs := float64(wait) / float64(time.Millisecond)
	busy := &floatHeap{}
	var queue []float64 // arrival times waiting for a slot, oldest first
	var skipped, served int
	expire := func(now float64) {
		for len(queue) > 0 && queue[0]+waitMs < now {
			queue = queue[1:]
			skipped++
		}
	}
	end := float64(seconds) * 1000
	for t := rng.ExpFloat64() * 1000 / rate; t < end; t += rng.ExpFloat64() * 1000 / rate {
		for busy.Len() > 0 && (*busy)[0] <= t {
			freed := heap.Pop(busy).(float64)
			expire(freed)
			if len(queue) > 0 {
				queue = queue[1:]
				heap.Push(busy, freed+check())
				served++
			}
		}
		expire(t)
		switch {
		case busy.Len() < slots:
			heap.Push(busy, t+check())
			served++
		case waitMs > 0:
			queue = append(queue, t)
		default:
			skipped++
		}
	}
	skipped += len(queue)
	return float64(skipped) / float64(skipped+served)
}

type floatHeap []float64

func (h floatHeap) Len() int           { return len(h) }
func (h floatHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h floatHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *floatHeap) Push(x any)        { *h = append(*h, x.(float64)) }
func (h *floatHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// adr: 831 amendment 11 — the model reproduces the skips amendment 10
// measured with two slots and no wait on gregale-compute-2 (n2-standard-4),
// and with the shipped sizing for that node predicts skips under 0.1% up to
// 500 req/s.
func TestInlineSlotModel(t *testing.T) {
	for _, tc := range []struct {
		rate     float64
		min, max float64 // amendment 10's measured @grx range, widened by a third
	}{
		{100, 0.0, 0.013},
		{250, 0.017, 0.043},
		{500, 0.045, 0.10},
		{1000, 0.14, 0.29},
	} {
		got := slotModel(tc.rate, 2, 0, 120, 1)
		t.Logf("2 slots, no wait, %.0f req/s: %.2f%% skipped", tc.rate, 100*got)
		if got < tc.min || got > tc.max {
			t.Errorf("2 slots, no wait, %.0f req/s: model skips %.2f%%, measured range %.1f-%.1f%%", tc.rate, 100*got, 100*tc.min, 100*tc.max)
		}
	}
	slots := api.EdgeWAFInlineSlots(4) // gregale-compute-2's processors
	for _, rate := range []float64{100, 250, 500, 1000} {
		got := slotModel(rate, slots, api.EdgeWAFInlineSlotWait, 120, 1)
		t.Logf("%d slots, %v wait, %.0f req/s: %.3f%% skipped", slots, api.EdgeWAFInlineSlotWait, rate, 100*got)
		if rate <= 500 && got > 0.001 {
			t.Errorf("%d slots, %v wait, %.0f req/s: model skips %.3f%%, want under 0.1%%", slots, api.EdgeWAFInlineSlotWait, rate, 100*got)
		}
	}
}
