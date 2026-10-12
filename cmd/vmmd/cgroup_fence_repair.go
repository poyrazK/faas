package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// cgroupFenceRepairInterval bounds how long a live VM can run without its
// memory and CPU fence after something outside vmmd resets the cgroup, such
// as a systemd daemon-reload disabling the slice's controllers.
const cgroupFenceRepairInterval = 5 * time.Second

type cgroupFenceRepairer interface {
	RepairCgroupFences(ctx context.Context) fcvm.CgroupFenceRepairReport
}

// runCgroupFenceRepair restores lost per-VM cgroup fences every interval
// until ctx ends.
func runCgroupFenceRepair(ctx context.Context, log *slog.Logger, r cgroupFenceRepairer, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			rep := r.RepairCgroupFences(ctx)
			if rep.Repaired > 0 || rep.Failed > 0 {
				log.Warn("vmmd: cgroup fence repair",
					"checked", rep.Checked, "repaired", rep.Repaired,
					"failed", rep.Failed, "gone", rep.Gone)
			}
		}
	}
}
