package instancestats

import (
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// rssHistory keeps each instance's resident memory over the last
// api.MeasuredChargeWindowSeconds, at most one sample per
// api.MeasuredChargeSampleSeconds, so the ADR-909 charge covers a VM's recent
// peak and not only its latest sample. Only the poller's tick goroutine uses
// it.
type rssHistory struct {
	byInstance map[string][]rssSample
}

type rssSample struct {
	at time.Time
	mb float64
}

func newRSSHistory() *rssHistory {
	return &rssHistory{byInstance: map[string][]rssSample{}}
}

// chargeSample records mb for instance at now and returns the sample the
// charge uses: the larger of mb and the window's p95.
func (h *rssHistory) chargeSample(instance string, mb float64, now time.Time) float64 {
	window := time.Duration(api.MeasuredChargeWindowSeconds) * time.Second
	every := time.Duration(api.MeasuredChargeSampleSeconds) * time.Second
	samples := h.byInstance[instance]
	keep := 0
	for keep < len(samples) && now.Sub(samples[keep].at) > window {
		keep++
	}
	samples = samples[keep:]
	if len(samples) == 0 || now.Sub(samples[len(samples)-1].at) >= every {
		samples = append(samples, rssSample{at: now, mb: mb})
	}
	h.byInstance[instance] = samples
	values := make([]float64, len(samples))
	for i, s := range samples {
		values[i] = s.mb
	}
	sort.Float64s(values)
	p := values[int(math.Ceil(float64(len(values))*api.MeasuredChargePercentile/100))-1]
	return math.Max(mb, p)
}

// retain drops the history of instances no longer listed.
func (h *rssHistory) retain(live map[string]struct{}) {
	for id := range h.byInstance {
		if _, ok := live[id]; !ok {
			delete(h.byInstance, id)
		}
	}
}
