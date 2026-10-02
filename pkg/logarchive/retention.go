package logarchive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/prometheus/client_golang/prometheus"
)

type ArchiveDeleter interface {
	Delete(context.Context, string) error
}

type RetentionParams struct {
	Store     state.RuntimeLogArchiveRetentionStore
	Archives  ArchiveDeleter
	Interval  time.Duration
	Timeout   time.Duration
	BatchSize int
	Now       func() time.Time
	Log       *slog.Logger
	Registry  prometheus.Registerer
}

type RetentionResult struct {
	Scanned, Collected int
}

// RetentionCollector collects canonical catalog reservations, including PUTs
// that committed without publication metadata. Unknown objects require verified
// ownership backfill; a storage listing alone never grants permission to delete.
type RetentionCollector struct {
	mu                      sync.Mutex
	p                       RetentionParams
	afterDay, afterInstance string
	metrics                 retentionMetrics
}

func NewRetentionCollector(p RetentionParams) (*RetentionCollector, error) {
	if p.Store == nil || p.Archives == nil {
		return nil, errors.New("logarchive: retention requires catalog and archive store")
	}
	if p.Interval <= 0 {
		p.Interval = DefaultFlushInterval
	}
	if p.Timeout <= 0 {
		p.Timeout = time.Minute
	}
	if p.BatchSize == 0 {
		p.BatchSize = state.RuntimeLogArchiveRetentionBatchMax
	}
	if p.BatchSize < 1 || p.BatchSize > state.RuntimeLogArchiveRetentionBatchMax {
		return nil, errors.New("logarchive: invalid retention batch size")
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	return &RetentionCollector{p: p, afterDay: "0001-01-01", metrics: newRetentionMetrics(p.Registry)}, nil
}

// RemoteRetentionDays derives the physical retention window from the longest
// current plan. The gateway continues to enforce each plan's shorter read cap.
// Local spool settings never shorten remote customer history.
func RemoteRetentionDays() int {
	var days int
	for _, plan := range api.Plans {
		if limit := plan.LogArchiveRetentionDaysMax(); limit > days {
			days = limit
		}
	}
	return days
}

func (c *RetentionCollector) Run(ctx context.Context) {
	run := func() {
		result, err := c.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			c.p.Log.Warn("logarchive.retention_failed", "err", err, "scanned", result.Scanned, "collected", result.Collected)
		} else if result.Collected > 0 {
			c.p.Log.Info("logarchive.retention_complete", "scanned", result.Scanned, "collected", result.Collected)
		}
	}
	run()
	ticker := time.NewTicker(c.p.Interval)
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

// RunOnce visits at most BatchSize descriptors. The cursor advances past failed
// routes and wraps after the final page, so one outage cannot starve other regions.
func (c *RetentionCollector) RunOnce(ctx context.Context) (RetentionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, c.p.Timeout)
	defer cancel()
	var result RetentionResult
	now := c.p.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	before := today.AddDate(0, 0, -RemoteRetentionDays()+1).Format("2006-01-02")
	refs, err := c.p.Store.ListRuntimeLogArchiveRetentionCandidates(ctx, state.RuntimeLogArchiveRetentionQuery{BeforeDay: before, AfterDay: c.afterDay, AfterInstance: c.afterInstance, Limit: c.p.BatchSize})
	if err != nil {
		c.metrics.failure("list")
		return result, fmt.Errorf("list expired archives: %w", err)
	}
	var failures []error
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break // Resume at the first unvisited descriptor next pass.
		}
		result.Scanned++
		if err := c.collect(ctx, ref); err != nil {
			failures = append(failures, fmt.Errorf("collect %s: %w", ref.StorageKey, err))
		} else {
			result.Collected++
		}
		c.afterDay, c.afterInstance = ref.Day, ref.InstanceID
	}
	if len(refs) < c.p.BatchSize && result.Scanned == len(refs) {
		c.afterDay, c.afterInstance = "0001-01-01", ""
	}
	c.metrics.observe(result, len(failures) == 0, c.p.Now())
	return result, errors.Join(failures...)
}

func (c *RetentionCollector) collect(ctx context.Context, ref state.RuntimeLogArchive) error {
	if !ref.ValidReference() {
		c.metrics.failure("retire")
		return errors.New("invalid archive reference")
	}
	unlock, err := c.p.Store.AcquireRuntimeLogArchivePublicationLock(ctx, ref.AppID)
	if err != nil {
		c.metrics.failure("lock")
		return err
	}
	defer unlock(context.WithoutCancel(ctx))
	if err := c.p.Store.RetireRuntimeLogArchive(ctx, ref); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil // Hard deletion or another collector already collected it.
		}
		c.metrics.failure("retire")
		return err
	}
	if err := c.p.Archives.Delete(ctx, ref.StorageKey); err != nil && !storage.IsNotFound(err) {
		c.metrics.failure("delete_object")
		return err // Keep the reservation and receipts for retry.
	}
	if err := c.p.Store.DeleteCollectedRuntimeLogArchive(ctx, ref); err != nil && !errors.Is(err, state.ErrNotFound) {
		c.metrics.failure("delete_metadata")
		return err
	}
	return nil
}
