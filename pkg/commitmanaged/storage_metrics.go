package commitmanaged

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type receiptStorageStore interface {
	CommitReceiptStorage(context.Context) (state.CommitReceiptUsage, error)
	ReconcileCommitReceiptStorage(context.Context) (bool, error)
}

// ReceiptStorageCollector samples the ledger independently of scrapes. A
// timed-out or stale sample never presents an old count as a current value.
type ReceiptStorageCollector struct {
	store                                                     receiptStorageStore
	mu                                                        sync.RWMutex
	usage                                                     state.CommitReceiptUsage
	checked                                                   time.Time
	ok                                                        bool
	success, countDesc, bytesDesc, shardsDesc, reconciledDesc *prometheus.Desc
	reconcileFailures                                         prometheus.Counter
}

func NewReceiptStorageCollector(store receiptStorageStore) *ReceiptStorageCollector {
	return &ReceiptStorageCollector{store: store,
		success:        prometheus.NewDesc("schedd_commit_receipt_storage_sample_success", "Whether a recent receipt storage sample succeeded.", nil, nil),
		countDesc:      prometheus.NewDesc("schedd_commit_receipt_count", "Receipt count in the platform ledger.", nil, nil),
		bytesDesc:      prometheus.NewDesc("schedd_commit_receipt_payload_json_bytes", "Bytes in PostgreSQL JSON text for retained receipt payloads, excluding other columns and indexes.", nil, nil),
		shardsDesc:     prometheus.NewDesc("schedd_commit_receipt_usage_initialized_shards", "Receipt usage shards initialized by backfill, out of 256.", nil, nil),
		reconciledDesc: prometheus.NewDesc("schedd_commit_receipt_usage_reconciliation_current", "Whether all shards have been reconciled within the last 25 hours.", nil, nil),
		reconcileFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "schedd_commit_receipt_reconciliation_failures_total",
			Help: "Receipt reconciliation attempts that failed on this scheduler.",
		})}
}

func (c *ReceiptStorageCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.success
	ch <- c.countDesc
	ch <- c.bytesDesc
	ch <- c.shardsDesc
	ch <- c.reconciledDesc
	c.reconcileFailures.Describe(ch)
}

func (c *ReceiptStorageCollector) Collect(ch chan<- prometheus.Metric) {
	c.reconcileFailures.Collect(ch)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.ok || time.Since(c.checked) > 30*time.Minute {
		ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 0)
		ch <- prometheus.MustNewConstMetric(c.reconciledDesc, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.shardsDesc, prometheus.GaugeValue, float64(c.usage.InitializedShards))
	if c.usage.InitializedShards != 256 || c.usage.TotalShards != 256 || c.usage.OldestReconciledAt == nil {
		ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 0)
		ch <- prometheus.MustNewConstMetric(c.reconciledDesc, prometheus.GaugeValue, 0)
		return
	}
	reconciled := float64(0)
	if !c.usage.OldestReconciledAt.After(time.Now().Add(time.Minute)) && time.Since(*c.usage.OldestReconciledAt) <= 25*time.Hour {
		reconciled = 1
	}
	ch <- prometheus.MustNewConstMetric(c.reconciledDesc, prometheus.GaugeValue, reconciled)
	ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(c.countDesc, prometheus.GaugeValue, float64(c.usage.Count))
	ch <- prometheus.MustNewConstMetric(c.bytesDesc, prometheus.GaugeValue, float64(c.usage.PayloadJSONBytes))
}

func (c *ReceiptStorageCollector) sample(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	usage, err := c.store.CommitReceiptStorage(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usage, c.checked, c.ok = usage, time.Now(), err == nil
}

func (c *ReceiptStorageCollector) Run(ctx context.Context) {
	for ctx.Err() == nil {
		reconcileCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		worked, err := c.store.ReconcileCommitReceiptStorage(reconcileCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			c.reconcileFailures.Inc()
		}
		c.sample(ctx)
		delay := time.Minute
		if worked {
			delay = 250 * time.Millisecond
		}
		c.mu.RLock()
		incomplete := c.ok && (c.usage.InitializedShards != 256 || c.usage.TotalShards != 256)
		c.mu.RUnlock()
		if !worked && incomplete {
			delay = time.Second
		}
		if err != nil {
			// A failed claim is quarantined in PostgreSQL; continue with other
			// shards without spinning when the database itself is unavailable.
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
