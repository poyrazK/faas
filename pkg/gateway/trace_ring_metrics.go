package gateway

import "github.com/prometheus/client_golang/prometheus"

func registerTraceRingMetrics(reg prometheus.Registerer, prefix string, ring *TraceRing) error {
	if reg == nil {
		return nil
	}
	if prefix != "" {
		prefix += "_"
	}
	metrics := []prometheus.Collector{
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: prefix + "trace_ring_bytes", Help: "Conservatively accounted retained trace bytes; not process RSS."}, func() float64 { return float64(ring.Stats().Bytes) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: prefix + "trace_ring_traces", Help: "Currently retained trace trees."}, func() float64 { return float64(ring.Stats().Traces) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: prefix + "trace_ring_evicted_total", Help: "Trace trees evicted by retention time, count or byte bounds."}, func() float64 { return float64(ring.Stats().Evicted) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: prefix + "trace_ring_rejected_total", Help: "Trace batches rejected because one tree exceeds the byte budget."}, func() float64 { return float64(ring.Stats().Rejected) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: prefix + "trace_ring_spans_dropped_total", Help: "New spans omitted at the per-trace retention cap."}, func() float64 { return float64(ring.Stats().DroppedSpans) }),
	}
	for _, metric := range metrics {
		if err := reg.Register(metric); err != nil {
			return err
		}
	}
	return nil
}
