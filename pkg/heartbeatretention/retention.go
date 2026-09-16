// Package heartbeatretention bounds raw compute heartbeat history while
// preserving compact hourly capacity evidence.
package heartbeatretention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	DefaultRawRetention = 7 * 24 * time.Hour
	DefaultInterval     = time.Hour
	DefaultBatchSize    = 5000
	DefaultMaxBatches   = 8
)

// Store is deliberately narrower than state.Store so the maintenance worker
// cannot gain unrelated mutation authority.
type Store interface {
	MaintainComputeNodeHeartbeatHistory(context.Context, time.Time, int) (state.ComputeNodeHeartbeatMaintenanceResult, error)
}

type Metrics struct {
	lastSuccess prometheus.Gauge
	oldestAge   prometheus.Gauge
	deleted     prometheus.Counter
	rollups     prometheus.Counter
	failures    prometheus.Counter
}

func NewMetrics(reg prometheus.Registerer, prefix string) *Metrics {
	if reg == nil {
		return nil
	}
	metric := func(suffix string) string { return prefix + "_compute_heartbeat_retention_" + suffix }
	m := &Metrics{
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: metric("last_success_timestamp_seconds"), Help: "Unix timestamp of the last successful raw compute-heartbeat rollup and retention pass."}),
		oldestAge:   prometheus.NewGauge(prometheus.GaugeOpts{Name: metric("oldest_raw_age_seconds"), Help: "Age in seconds of the oldest raw compute heartbeat remaining after the latest retention pass; zero when none remain."}),
		deleted:     prometheus.NewCounter(prometheus.CounterOpts{Name: metric("rows_deleted_total"), Help: "Raw compute heartbeat rows deleted after durable hourly rollup."}),
		rollups:     prometheus.NewCounter(prometheus.CounterOpts{Name: metric("rollup_buckets_total"), Help: "Hourly compute heartbeat buckets inserted or updated by retention transactions."}),
		failures:    prometheus.NewCounter(prometheus.CounterOpts{Name: metric("failures_total"), Help: "Failed raw compute-heartbeat retention passes."}),
	}
	reg.MustRegister(m.lastSuccess, m.oldestAge, m.deleted, m.rollups, m.failures)
	return m
}

type Cleanup struct {
	store      Store
	log        *slog.Logger
	metrics    *Metrics
	now        func() time.Time
	retention  time.Duration
	interval   time.Duration
	batchSize  int
	maxBatches int
}

func New(store Store, log *slog.Logger, metrics *Metrics) *Cleanup {
	if store == nil {
		panic("heartbeatretention: store is required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Cleanup{store: store, log: log, metrics: metrics, now: time.Now, retention: DefaultRawRetention, interval: DefaultInterval, batchSize: DefaultBatchSize, maxBatches: DefaultMaxBatches}
}

// WithPolicy overrides defaults for tests and exceptional operator recovery.
func (c *Cleanup) WithPolicy(retention, interval time.Duration, batchSize, maxBatches int) *Cleanup {
	if retention > 0 {
		c.retention = retention
	}
	if interval > 0 {
		c.interval = interval
	}
	if batchSize > 0 {
		c.batchSize = batchSize
	}
	if maxBatches > 0 {
		c.maxBatches = maxBatches
	}
	return c
}

func (c *Cleanup) WithClock(now func() time.Time) *Cleanup {
	if now != nil {
		c.now = now
	}
	return c
}

// SweepOnce drains at most maxBatches independently committed batches. Each
// transaction uses SKIP LOCKED and remains bounded by batchSize.
func (c *Cleanup) SweepOnce(ctx context.Context) (state.ComputeNodeHeartbeatMaintenanceResult, error) {
	now := c.now().UTC()
	cutoff := now.Add(-c.retention)
	var total state.ComputeNodeHeartbeatMaintenanceResult
	for batch := 0; batch < c.maxBatches; batch++ {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		result, err := c.store.MaintainComputeNodeHeartbeatHistory(ctx, cutoff, c.batchSize)
		if err != nil {
			if c.metrics != nil {
				c.metrics.failures.Inc()
			}
			return total, fmt.Errorf("compute heartbeat retention: %w", err)
		}
		total.Deleted += result.Deleted
		total.RollupBuckets += result.RollupBuckets
		total.OldestRawAt = result.OldestRawAt
		if result.Deleted < int64(c.batchSize) {
			break
		}
	}
	if c.metrics != nil {
		c.metrics.deleted.Add(float64(total.Deleted))
		c.metrics.rollups.Add(float64(total.RollupBuckets))
		c.metrics.lastSuccess.Set(float64(now.Unix()))
		age := 0.0
		if !total.OldestRawAt.IsZero() && total.OldestRawAt.Before(now) {
			age = now.Sub(total.OldestRawAt).Seconds()
		}
		c.metrics.oldestAge.Set(age)
	}
	return total, nil
}

// Run performs an immediate pass, then repeats until shutdown. A transient
// failure is logged and retried; it never terminates schedd.
func (c *Cleanup) Run(ctx context.Context) {
	run := func() {
		result, err := c.SweepOnce(ctx)
		if err != nil {
			if ctx.Err() == nil {
				c.log.Warn("compute heartbeat retention failed", "err", err)
			}
			return
		}
		if result.Deleted > 0 {
			c.log.Info("compute heartbeat retention completed", "deleted", result.Deleted, "rollup_buckets", result.RollupBuckets)
		}
	}
	run()
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
