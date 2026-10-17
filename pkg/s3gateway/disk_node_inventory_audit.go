package s3gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/prometheus/client_golang/prometheus"
)

// RefreshInventoryStep never exports partial totals as an observation. Source
// damage withdraws health immediately; last complete gauges remain available.
func (h *DiskHandler) RefreshInventoryStep(ctx context.Context, options objectnode.InventoryAuditOptions) (objectnode.InventoryAuditStatus, error) {
	status, err := h.config.Store.AuditStep(ctx, options)
	if err != nil {
		h.metrics.scans.WithLabelValues("failed").Inc()
		h.metrics.healthy.Set(0)
		return status, err
	}
	if status.Usage.Errors != 0 {
		h.metrics.healthy.Set(0)
	}
	if status.State == "complete" {
		h.recordInventory(status.Usage, status.Usage.StartedAt)
	}
	return status, nil
}

func (h *DiskHandler) RunInventory(ctx context.Context, observe func(error)) {
	h.RunInventoryWithOptions(ctx, objectnode.DefaultInventoryAuditOptions(), observe)
}

func (h *DiskHandler) RunInventoryWithOptions(ctx context.Context, options objectnode.InventoryAuditOptions, observe func(error)) {
	if err := options.Validate(); err != nil {
		if observe != nil {
			observe(err)
		}
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		status, err := h.RefreshInventoryStep(ctx, options)
		if errors.Is(err, objectnode.ErrFenced) || errors.Is(err, objectnode.ErrReplica) || errors.Is(err, objectnode.ErrClosed) {
			return
		}
		delay := api.ObjectNodeAuditStepInterval
		if err != nil || status.State == "complete" {
			delay = api.ObjectNodeInventoryInterval
			if err == nil && !status.Usage.CatalogHealthy {
				err = fmt.Errorf("object node: inventory found %d catalog errors", status.Usage.Errors)
			}
			if observe != nil && ctx.Err() == nil {
				observe(err)
			}
		}
		timer.Reset(delay)
	}
}

func registerInventoryAuditMetrics(h *DiskHandler, registry *prometheus.Registry) {
	for name, read := range map[string]func(objectnode.InventoryAuditStatus) float64{
		"running":         func(s objectnode.InventoryAuditStatus) float64 { return recoveryCleanupBool(s.State == "running") },
		"step_records":    func(s objectnode.InventoryAuditStatus) float64 { return float64(s.StepRecords) },
		"pending_records": func(s objectnode.InventoryAuditStatus) float64 { return float64(s.Pending) },
		"backlog_age_seconds": func(s objectnode.InventoryAuditStatus) float64 {
			if s.BacklogSince.IsZero() {
				return 0
			}
			return max(0, time.Since(s.BacklogSince).Seconds())
		},
		"started_timestamp_seconds": func(s objectnode.InventoryAuditStatus) float64 {
			return inventoryAuditTimestamp(s.Usage.StartedAt)
		},
		"last_complete_timestamp_seconds": func(s objectnode.InventoryAuditStatus) float64 {
			return inventoryAuditTimestamp(s.LastCompletedAt)
		},
		"last_healthy_timestamp_seconds": func(s objectnode.InventoryAuditStatus) float64 {
			return inventoryAuditTimestamp(s.LastHealthyAt)
		},
	} {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_inventory_audit_" + name, Help: "Cached incremental source audit progress; partial work does not refresh health."}, func() float64 {
			return read(h.config.Store.InventoryAuditStatus())
		}))
	}
	for name, read := range map[string]func(objectnode.InventoryAuditStatus) uint64{
		"steps":      func(s objectnode.InventoryAuditStatus) uint64 { return s.Steps },
		"examined":   func(s objectnode.InventoryAuditStatus) uint64 { return s.Examined },
		"restarts":   func(s objectnode.InventoryAuditStatus) uint64 { return s.Restarts },
		"cycles":     func(s objectnode.InventoryAuditStatus) uint64 { return s.Cycles },
		"failures":   func(s objectnode.InventoryAuditStatus) uint64 { return s.Failures },
		"reconciled": func(s objectnode.InventoryAuditStatus) uint64 { return s.Reconciled },
		"overflows":  func(s objectnode.InventoryAuditStatus) uint64 { return s.Overflows },
	} {
		registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "faas_object_node_inventory_audit_" + name + "_total", Help: "Process-local incremental audit work without identity labels."}, func() float64 {
			return float64(read(h.config.Store.InventoryAuditStatus()))
		}))
	}
}

func inventoryAuditTimestamp(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix())
}
