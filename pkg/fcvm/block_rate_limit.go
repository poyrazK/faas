package fcvm

// Per-plan block rate limits (ADR-910). A VM's writable app-layer drive gets
// Firecracker token buckets on bandwidth and operations, so one tenant
// flooding its disk cannot starve its neighbours' I/O on the shared host
// disk. The read-only base and sidecar drives are not limited: they are
// shared and mostly served from the host page cache.

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/api"
)

// blockRateLimiter returns plan's limiter, or nil for an unknown plan.
func blockRateLimiter(plan api.Plan) *RateLimiter {
	limit, ok := api.BlockRateLimitPerPlan[plan]
	if !ok {
		return nil
	}
	return &RateLimiter{
		Bandwidth: &TokenBucket{Size: int64(limit.BandwidthMBps) << 20, OneTimeBurst: int64(api.BlockRateLimitBurstMB) << 20, RefillTimeMs: 1000},
		Ops:       &TokenBucket{Size: int64(limit.IOPS), OneTimeBurst: api.BlockRateLimitBurstOps, RefillTimeMs: 1000},
	}
}

// writableLayerDrive reports whether d is a VM's writable app layer.
func writableLayerDrive(d Drive) bool {
	return !d.IsRootDevice && !d.IsReadOnly && (d.DriveID == DriveLayer || d.DriveID == DriveLayerMain)
}

// withBlockRateLimit sets the lease's plan limiter on the writable app-layer
// drive. Builder VMs keep unlimited drives: their slots already bound them.
func withBlockRateLimit(l Lease, cfg VMConfig) VMConfig {
	limiter := blockRateLimiter(l.Plan)
	if l.IsBuilder || limiter == nil {
		return cfg
	}
	drives := append([]Drive(nil), cfg.Drives...)
	for i := range drives {
		if writableLayerDrive(drives[i]) {
			drives[i].RateLimiter = limiter
		}
	}
	cfg.Drives = drives
	return cfg
}

// applyBlockRateLimitAfterRestore sets the plan limiter on a restored VM's
// writable layer, which also covers snapshots captured before ADR-910. It
// runs after readiness, off the wake's critical path; a failure only logs.
func (v *JailerVMM) applyBlockRateLimitAfterRestore(ctx context.Context, l Lease, workloads []WorkloadSpec) {
	limiter := blockRateLimiter(l.Plan)
	if l.IsBuilder || limiter == nil {
		return
	}
	driveID := restoredLayerDriveID(workloads)
	body := map[string]any{"drive_id": driveID, "rate_limiter": limiter}
	if err := v.apiPatch(ctx, l.Instance, "/drives/"+driveID, body); err != nil {
		slog.Default().Warn("vmm: block rate limit after restore", "instance", l.Instance, "drive", driveID, "err", err)
	}
}

// restoredLayerDriveID is the writable layer's drive id, as BuildColdBootConfig
// named it when the snapshot's VM booted.
func restoredLayerDriveID(workloads []WorkloadSpec) string {
	if len(workloads) == 0 {
		return DriveLayer
	}
	if workloads[0].DriveID != "" {
		return workloads[0].DriveID
	}
	return DriveLayerMain
}
