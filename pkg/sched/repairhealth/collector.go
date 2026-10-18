// Package repairhealth samples durable repair health independently of VM work.
package repairhealth

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	counts, oldest, warnings *prometheus.GaugeVec
	held                     *prometheus.GaugeVec
	unavailable, lastSuccess prometheus.Gauge
	collections              *prometheus.CounterVec
}

func NewMetrics(reg prometheus.Registerer, prefix string) *Metrics {
	m := &Metrics{
		counts:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_data_repair_phase_count", Help: "Active durable repairs by phase; fleet snapshot, not per-scheduler ownership."}, []string{"phase"}),
		oldest:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_data_repair_oldest_phase_timestamp_seconds", Help: "Oldest phase entry time among active repairs; zero when none exist."}, []string{"phase"}),
		warnings:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_data_repair_phase_warn_after_seconds", Help: "Phase age warning threshold from the shared repair limits."}, []string{"phase"}),
		held:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_data_repair_capacity_held", Help: "Capacity retained by durable running/cleanup tasks; not measured host consumption."}, []string{"resource"}),
		unavailable: prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_data_repair_unavailable_tasks", Help: "Capacity-holding repair tasks with unavailable, missing, retired or stale-heartbeat nodes."}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_data_repair_health_last_success_timestamp_seconds", Help: "Last successful complete durable repair health observation; zero until first success."}),
		collections: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_data_repair_health_collection_total", Help: "Repair health collection attempts by bounded outcome."}, []string{"outcome"}),
	}
	for _, phase := range api.DataRepairHealthPhases() {
		m.counts.WithLabelValues(phase)
		m.oldest.WithLabelValues(phase)
		m.warnings.WithLabelValues(phase).Set(float64(api.DataRepairHealthWarnSeconds(phase)))
	}
	for _, resource := range []string{"slots", "memory_mb", "vcpus", "cpu_millicores"} {
		m.held.WithLabelValues(resource)
	}
	for _, outcome := range []string{"success", "error"} {
		m.collections.WithLabelValues(outcome)
	}
	reg.MustRegister(m.counts, m.oldest, m.warnings, m.held, m.unavailable, m.lastSuccess, m.collections)
	return m
}

type Collector struct {
	Store   state.DataRepairHealthStore
	Metrics *Metrics
	Log     *slog.Logger
}

func (c *Collector) ObserveOnce(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, time.Duration(api.DataRepairHealthQuerySeconds)*time.Second)
	defer cancel()
	h, err := c.Store.DataRepairHealth(ctx, time.Now().UTC(), "", 0)
	if err == nil && !completeSnapshot(h) {
		err = state.ErrInvalidArgument
	}
	if err != nil {
		c.Metrics.collections.WithLabelValues("error").Inc()
		return err // Previous gauges and success time survive a failed read.
	}
	for _, p := range h.Phases {
		c.Metrics.counts.WithLabelValues(p.Phase).Set(float64(p.Count))
		at := float64(0)
		if p.OldestPhaseStartedAt != nil {
			at = float64(p.OldestPhaseStartedAt.Unix())
		}
		c.Metrics.oldest.WithLabelValues(p.Phase).Set(at)
	}
	c.Metrics.held.WithLabelValues("slots").Set(float64(h.HeldSlots))
	c.Metrics.held.WithLabelValues("memory_mb").Set(float64(h.HeldMemoryMB))
	c.Metrics.held.WithLabelValues("vcpus").Set(float64(h.HeldVCPUs))
	c.Metrics.held.WithLabelValues("cpu_millicores").Set(float64(h.HeldCPUMillicores))
	c.Metrics.unavailable.Set(float64(h.UnavailableTasks))
	c.Metrics.lastSuccess.Set(float64(h.ObservedAt.Unix()))
	c.Metrics.collections.WithLabelValues("success").Inc()
	return nil
}

func completeSnapshot(h api.DataRepairHealth) bool {
	if h.ObservedAt.IsZero() || len(h.Phases) != len(api.DataRepairHealthPhases()) {
		return false
	}
	seen := map[string]bool{}
	for _, p := range h.Phases {
		if !validPhase(p.Phase) || seen[p.Phase] || p.Count < 0 || (p.Count > 0 && (p.OldestPhaseStartedAt == nil || p.OldestPhaseStartedAt.IsZero())) || (p.Count == 0 && p.OldestPhaseStartedAt != nil) {
			return false
		}
		seen[p.Phase] = true
	}
	return true
}

func validPhase(phase string) bool {
	for _, p := range api.DataRepairHealthPhases() {
		if p == phase {
			return true
		}
	}
	return false
}

func (c *Collector) Run(ctx context.Context) {
	tick := time.NewTicker(time.Duration(api.DataRepairHealthPollSeconds) * time.Second)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := c.ObserveOnce(ctx); err != nil && ctx.Err() == nil && c.Log != nil {
			c.Log.Warn("repair health observation deferred", "error_class", "unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
