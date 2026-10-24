package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/sched/instancestats"
)

// cpuSaturationCaptures queues automatic CPU captures of instances the stats
// poller saw at their CPU quota (ADR-1155). Quotas come from the admission
// ledger, so the per-tick check never reads the store.
type cpuSaturationCaptures struct {
	engine *sched.Engine
	log    *slog.Logger
}

func (c cpuSaturationCaptures) CPUQuotaMillicores(instanceID string, now time.Time) (int, bool) {
	return c.engine.Ledger().SustainedCPUMillicores(instanceID, now)
}

func (c cpuSaturationCaptures) CPUSaturated(ctx context.Context, row instancestats.InstanceStat, usedMillicores, quotaMillicores int, sustained time.Duration) error {
	queued, reason, err := c.engine.QueueCPUSaturationCapture(ctx, row.InstanceID, usedMillicores, quotaMillicores, sustained)
	if err != nil {
		return err
	}
	c.log.Info("sched: cpu saturation capture", "instance_id", row.InstanceID, "app_id", row.AppID, "queued", queued, "reason", reason,
		"used_millicores", usedMillicores, "quota_millicores", quotaMillicores, "sustained", sustained)
	return nil
}
