// adr: 431
package gateway

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

func budgetTrace(id string, size int) *Trace {
	return &Trace{TraceID: id, Spans: []SpanRow{{SpanID: "a", Attributes: map[string]string{"url": strings.Repeat("x", size)}}}}
}

func TestTraceRingByteBudgetEvictsAndRejects(t *testing.T) {
	r := NewTraceRingWithBudget(100, 2048)
	r.Add(budgetTrace("first", 500))
	r.Add(budgetTrace("second", 500))
	if _, ok := r.Get("first"); ok {
		t.Fatal("byte budget did not evict oldest trace before entry cap")
	}
	if _, ok := r.Get("second"); !ok {
		t.Fatal("new trace should fit")
	}
	before := r.Stats()
	if r.Add(budgetTrace("oversized", 4096)) {
		t.Fatal("accepted one trace larger than the entire budget")
	}
	if got := r.Stats(); got.Bytes != before.Bytes || got.Rejected != 1 || got.Evicted != 1 {
		t.Fatalf("oversized trace changed retention: %+v -> %+v", before, got)
	}
}

func TestTraceRingMergedTraceBudget(t *testing.T) {
	r := NewTraceRingWithBudget(100, 2048)
	r.Add(budgetTrace("other", 100))
	r.Add(budgetTrace("merge", 100))
	add := budgetTrace("merge", 600)
	add.Spans[0].SpanID = "b"
	r.Add(add)
	if _, ok := r.Get("other"); ok {
		t.Fatal("growing existing trace did not evict oldest trace")
	}
	add.Spans[0].SpanID = "c"
	if r.Add(add) {
		t.Fatal("accepted oversized merged trace")
	}
	got, _ := r.Get("merge")
	if len(got.Spans) != 2 {
		t.Fatalf("rejected merge changed existing spans: %d", len(got.Spans))
	}
}

func TestTraceRingCapsOneRepeatedTraceAndOwnsAttributes(t *testing.T) {
	r := NewTraceRing(100)
	trace := budgetTrace("same", 1)
	r.Add(trace)
	trace.Spans[0].Attributes["url"] = strings.Repeat("x", 100000)
	for i := 0; i < api.TraceRingMaxSpansPerTrace+10; i++ {
		r.Add(&Trace{TraceID: "same", Spans: []SpanRow{{SpanID: fmt.Sprint(i)}}})
	}
	got, _ := r.Get("same")
	if len(got.Spans) != api.TraceRingMaxSpansPerTrace || got.Spans[0].Attributes["url"] != "x" {
		t.Fatal("span cap or input attribute ownership failed")
	}
	got.Spans[0].Attributes["url"] = "mutated"
	again, _ := r.Get("same")
	if again.Spans[0].Attributes["url"] != "x" {
		t.Fatal("Get leaked mutable attributes out of the byte budget")
	}
	if r.Stats().DroppedSpans == 0 {
		t.Fatal("span cap losses are invisible")
	}
}

// Random sequences exercise inserts, repeated-trace growth, retrieval, count
// and time eviction. Recompute accounting after every operation to catch drift.
func TestTraceRingBudgetProperty(t *testing.T) {
	r := NewTraceRingWithBudget(17, 8192)
	clock := time.Now()
	r.SetNowForTest(func() time.Time { return clock })
	rng := rand.New(rand.NewPCG(43, 17))
	for i := 0; i < 2000; i++ {
		id := fmt.Sprint(rng.IntN(40))
		trace := budgetTrace(id, rng.IntN(4000))
		trace.Spans[0].SpanID = fmt.Sprint(rng.IntN(8))
		r.Add(trace)
		r.Get(fmt.Sprint(rng.IntN(40)))
		if i%100 == 0 {
			clock = clock.Add(25 * time.Hour)
		}
		var bytes int64
		for el := r.ll.Front(); el != nil; el = el.Next() {
			entry := el.Value.(*traceRingEntry)
			bytes += traceRetainedBytes(entry.trace.TraceID, entry.trace.Spans)
		}
		if got := r.Stats(); got.Bytes != bytes || bytes > 8192 || got.Traces > 17 || bytes < 0 {
			t.Fatalf("operation %d violated retention bounds: %+v, recomputed %d", i, got, bytes)
		}
	}
}

func TestTraceRingRetentionMetrics(t *testing.T) {
	r := NewTraceRingWithBudget(1, 2048)
	reg := prometheus.NewRegistry()
	if err := registerTraceRingMetrics(reg, "gatewayd_public", r); err != nil {
		t.Fatal(err)
	}
	r.Add(budgetTrace("a", 1))
	r.Add(budgetTrace("b", 1))
	r.Add(budgetTrace("large", 4096))
	metrics, err := reg.Gather()
	if err != nil || len(metrics) != 5 {
		t.Fatalf("retention metrics: %d families, %v", len(metrics), err)
	}
	for _, m := range metrics {
		if strings.HasSuffix(m.GetName(), "_evicted_total") || strings.HasSuffix(m.GetName(), "_rejected_total") {
			if m.Metric[0].Counter.GetValue() != 1 {
				t.Fatalf("%s = %v", m.GetName(), m.Metric[0].Counter.GetValue())
			}
		}
	}
}
