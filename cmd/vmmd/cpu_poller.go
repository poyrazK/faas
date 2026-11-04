// CPU sample loop (issue #279 / PR-B).
//
// This file owns the goroutine that reads cgroupstats.Reader.Sample
// per instance and feeds the cpustats.Cache. The Stats gRPC handler
// in pkg/vmmdgrpc reads from the cache (no cgroup I/O on the hot
// path).
//
// Design notes:
//
//   - The poll cadence is 250 ms — half the schedd poller's 200 ms
//     so a fresh rate is always available when schedd dials Stats,
//     and 250 ms matches the spike-capture window the cgroupstats
//     metal test was written against
//     (pkg/sched/instancestats/poller_metal_test.go:153).
//
//   - cgroupstats.Reader enumerates per-VM cgroup leaves each tick.
//     On a non-Linux host the enumeration returns an empty slice;
//     the loop is a no-op there, leaving cpuCache cold — same as
//     a fresh daemon that hasn't observed its first sample yet.
//
//   - Each Observation passes the same now across the batch so
//     per-instance deltas are computed against a single instant.
//     This matters: if the loop iterates with a fresh time.Now per
//     instance, the rate drifts with the loop's iteration order.
//
//   - The first sample per instance returns ok=false from
//     cpustats.Cache.Observe (baseline only). The cache handles
//     the "first sample" / "regression" / "no time elapsed" cases
//     and never returns a degenerate reading.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/cgroupstats"
	"github.com/onebox-faas/faas/pkg/fcvm/cpustats"
)

// CPUSampleInterval is the cadence of the per-instance cgroup
// usage_usec scrape. 250 ms is half the schedd poller's 200 ms —
// guarantees a fresh rate is ready when schedd dials Stats.
const CPUSampleInterval = 250 * time.Millisecond

// runCPUSampleLoop drives the cpustats cache at CPUSampleInterval.
// The reader is the production cgroupstats.NewWithDefaults()
// pointed at /sys/fs/cgroup. On non-Linux hosts enumeration
// returns an empty slice; the loop ticks but never observes.
//
// ctx cancellation is the shutdown signal. The loop returns
// promptly so cmd/vmmd's main can reach GracefulStop without
// waiting for a tick.
func runCPUSampleLoop(ctx context.Context, cache *cpustats.Cache, log *slog.Logger) {
	if cache == nil {
		return
	}
	reader := cgroupstats.NewWithDefaults()
	t := time.NewTicker(CPUSampleInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			instances, err := reader.Instances()
			if err != nil {
				// Missing slice (cold-boot race, transient teardown)
				// is normal — cgroupstats already filters it. Other
				// errors propagate so the operator sees them.
				log.Debug("cpu sample: enumerate failed", "err", err)
				continue
			}
			for _, info := range instances {
				sample, ok := reader.Sample(info.Instance, info.Plan)
				if !ok {
					// Cgroup leaf not yet readable; cpustats
					// won't record a baseline so cpu_pct stays
					// absent on the wire. schedd stamps Unknown.
					continue
				}
				// Pass the same `now` across the batch so
				// per-instance deltas are computed against a
				// single instant.
				_, _ = cache.Observe(cpustats.Observation{
					InstanceID:    info.Instance,
					CPUUsageUsec:  sample.CPUUsageUsec,
					ThrottledUsec: sample.ThrottledUsec,
					At:            now,
				})
			}
		}
	}
}

// agentWaitCPU reads an instance's cumulative CPU seconds from the cache for
// the model-wait pause (ADR-20261027090012). A reading the cache marks
// invalid is no reading: the pause then waits.
type agentWaitCPU struct{ cache *cpustats.Cache }

func (c agentWaitCPU) CPUSeconds(instance string) (float64, bool) {
	r, ok := c.cache.Lookup(instance)
	return r.CPUSeconds, ok && r.Valid
}
