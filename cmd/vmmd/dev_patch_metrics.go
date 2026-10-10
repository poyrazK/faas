package main

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// devPatchMetrics measures developer live patch delivery (ADR-740): how
// patches end and how long a saved edit takes to reach the running VM.
// Only a generation's first acknowledgement is observed.
type devPatchMetrics struct {
	acks     *prometheus.CounterVec
	apply    prometheus.Histogram
	delivery *prometheus.HistogramVec
}

// devPatchAckResults bounds the result label: guest-init reports the error
// code, so anything outside its known codes is counted as "other".
var devPatchAckResults = []string{"applied", "invalid_patch", "apply_failed", "restart_failed", "other"}

func devPatchAckResult(errorCode string) string {
	if errorCode == "" {
		return "applied"
	}
	for _, known := range devPatchAckResults[1 : len(devPatchAckResults)-1] {
		if errorCode == known {
			return known
		}
	}
	return "other"
}

func newDevPatchMetrics(reg prometheus.Registerer) *devPatchMetrics {
	m := &devPatchMetrics{
		acks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_dev_patch_acks_total",
			Help: "First instance acknowledgements of developer live patches, by result.",
		}, []string{"result"}),
		apply: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "vmmd_dev_patch_apply_seconds",
			Help:    "Guest-reported time to write a developer live patch under /app.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
		delivery: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "vmmd_dev_patch_delivery_seconds",
			Help:    "Developer live patch publication to first instance acknowledgement, by result.",
			Buckets: []float64{0.25, 0.5, 1, 2, 3, 5, 10, 20, 30, 60, 120},
		}, []string{"result"}),
	}
	if reg == nil {
		return m
	}
	m.acks = registerDevPatchCollector(reg, m.acks).(*prometheus.CounterVec)
	m.apply = registerDevPatchCollector(reg, m.apply).(prometheus.Histogram)
	m.delivery = registerDevPatchCollector(reg, m.delivery).(*prometheus.HistogramVec)
	for _, result := range devPatchAckResults {
		m.acks.WithLabelValues(result)
	}
	return m
}

func registerDevPatchCollector(reg prometheus.Registerer, c prometheus.Collector) prometheus.Collector {
	if err := reg.Register(c); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			return already.ExistingCollector
		}
		panic(err)
	}
	return c
}

func (m *devPatchMetrics) observeAck(errorCode string, apply, delivery time.Duration) {
	if m == nil {
		return
	}
	result := devPatchAckResult(errorCode)
	m.acks.WithLabelValues(result).Inc()
	if result == "applied" {
		m.apply.Observe(apply.Seconds())
	}
	m.delivery.WithLabelValues(result).Observe(delivery.Seconds())
}
