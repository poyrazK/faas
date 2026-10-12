// adr: 909 — a measured charge covers an instance's recent peak: the larger
// of its latest resident memory and its 10-minute p95.
package instancestats

import (
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRSSHistoryChargeSample(t *testing.T) {
	h := newRSSHistory()
	start := time.Unix(1_800_000_000, 0)
	every := time.Duration(api.MeasuredChargeSampleSeconds) * time.Second
	at := func(i int) time.Time { return start.Add(time.Duration(i) * every) }
	// Two minutes at 600 MB, then the VM frees down to 200 MB.
	for i := 0; i < 24; i++ {
		h.chargeSample("a", 600, at(i))
	}
	if got := h.chargeSample("a", 200, at(24)); got != 600 {
		t.Fatalf("right after shrinking: charge sample %v, want the recent peak 600", got)
	}
	// Samples between ticks of the sampling interval are not recorded.
	if got := len(h.byInstance["a"]); got != 25 {
		t.Fatalf("history holds %d samples, want 25", got)
	}
	h.chargeSample("a", 200, at(24).Add(time.Second))
	if got := len(h.byInstance["a"]); got != 25 {
		t.Fatalf("a sample inside the interval was recorded: %d samples", got)
	}
	// Ten minutes later the peak has aged out.
	window := time.Duration(api.MeasuredChargeWindowSeconds) * time.Second
	var got float64
	for i := 25; at(i).Sub(at(23)) <= window+every; i++ {
		got = h.chargeSample("a", 200, at(i))
	}
	if got != 200 {
		t.Fatalf("after the window: charge sample %v, want 200", got)
	}
	// Growth is charged at once.
	if got := h.chargeSample("a", 900, at(400)); got != 900 {
		t.Fatalf("growing VM: charge sample %v, want 900", got)
	}
	h.retain(map[string]struct{}{})
	if len(h.byInstance) != 0 {
		t.Fatal("retain kept a departed instance")
	}
}

func TestPublishMemoryShadowChargesRecentPeak(t *testing.T) {
	r := NewReader()
	p := &Poller{Reader: r}
	instances := []state.Instance{{ID: "a", NodeID: "n1", State: string(state.StateRunning), RAMMB: 1024}}
	now := time.Unix(1_800_000_000, 0)
	p.publishMemoryShadow(instances, []InstanceStat{{InstanceID: "a", RSSMB: 600}}, now)
	p.publishMemoryShadow(instances, []InstanceStat{{InstanceID: "a", RSSMB: 200}}, now.Add(time.Minute))
	got, _, _ := r.MeasuredMB("n1", now.Add(time.Minute))
	if want := api.MeasuredChargeMB(1024, 600); got != want {
		t.Fatalf("measured %d MB after shrinking, want the recent peak's charge %d", got, want)
	}
	p.publishMemoryShadow(instances, []InstanceStat{{InstanceID: "a", RSSMB: math.NaN()}}, now.Add(2*time.Minute))
	got, _, _ = r.MeasuredMB("n1", now.Add(2*time.Minute))
	if want := api.BillableRAMMB(1024); got != want {
		t.Fatalf("unmeasured instance books %d MB, want its reservation %d", got, want)
	}
}
